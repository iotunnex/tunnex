package controllers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	tunnexv1 "github.com/tunnexio/tunnex/apps/operator/api/v1alpha1"
	"github.com/tunnexio/tunnex/apps/operator/internal/cp"
)

// ── harness ─────────────────────────────────────────────────────────────────────────────────────────────

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := tunnexv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func fakeK8s(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(objs...).
		WithStatusSubresource(&tunnexv1.TunnexCluster{}, &tunnexv1.TunnexExposedService{}, &tunnexv1.TunnexGrant{}).
		Build()
}

func testCP(t *testing.T, h http.HandlerFunc) *cp.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return cp.New(srv.URL, "tnxm_test", "org-123")
}

// cpDown is a CP that fails every call — used where a reconcile must return BEFORE any CP contact.
func cpDown(t *testing.T) *cp.Client {
	return testCP(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("CP must not be called, got %s %s", r.Method, r.URL.Path)
		http.Error(w, "boom", 500)
	})
}

func req(ns, name string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: name}}
}

func labeled() map[string]string { return map[string]string{managedByLabel: managedByValue} }

// managedMeta is a CR that has already had its first pass (label + finalizer present) — the state in which a
// reconcile proceeds to the CP instead of stopping to stamp metadata.
func managedMeta(ns, name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Namespace: ns, Name: name, Labels: labeled(), Finalizers: []string{finalizerName}}
}

func readyCond(t *testing.T, conds []metav1.Condition) *metav1.Condition {
	t.Helper()
	return apimeta.FindStatusCondition(conds, condReady)
}

// ── ownership label (first pass stamps + requeues, no CP contact) ───────────────────────────────────────

func TestOwnershipLabelStampedBeforeCP(t *testing.T) {
	cr := &tunnexv1.TunnexCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "c"}}
	k := fakeK8s(t, cr)
	r := &TunnexClusterReconciler{Client: k, CP: cpDown(t)}

	res, err := r.Reconcile(context.Background(), req("ns", "c"))
	if err != nil || !res.Requeue {
		t.Fatalf("first pass must label + requeue, got res=%+v err=%v", res, err)
	}
	var got tunnexv1.TunnexCluster
	if err := k.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "c"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Labels[managedByLabel] != managedByValue {
		t.Fatalf("ownership label not stamped: %v", got.Labels)
	}
}

// ── cluster: accepted / honest 4xx / keep-last 5xx / site_not_found ─────────────────────────────────────

func TestClusterReconcileAccepted(t *testing.T) {
	cp := testCP(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/sites"):
			json.NewEncoder(w).Encode([]map[string]string{{"id": "site-1", "name": "edge"}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/k8s/clusters"):
			json.NewEncoder(w).Encode([]map[string]string{})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/k8s/clusters"):
			json.NewEncoder(w).Encode(map[string]string{"id": "clu-1", "dns_vip": "100.64.0.53"})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	cr := &tunnexv1.TunnexCluster{
		ObjectMeta: managedMeta("ns", "c"),
		Spec:       tunnexv1.TunnexClusterSpec{Site: "edge", Name: "prod", VIPRange: "100.64.0.0/16", ServiceCIDR: "10.96.0.0/12", DNSZone: "k8s.acme.com"},
	}
	k := fakeK8s(t, cr)
	r := &TunnexClusterReconciler{Client: k, CP: cp}
	if _, err := r.Reconcile(context.Background(), req("ns", "c")); err != nil {
		t.Fatal(err)
	}
	var got tunnexv1.TunnexCluster
	k.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "c"}, &got)
	if got.Status.ClusterID != "clu-1" || got.Status.DNSVIP != "100.64.0.53" {
		t.Fatalf("derived status not mirrored: %+v", got.Status)
	}
	if c := readyCond(t, got.Status.Conditions); c == nil || c.Status != metav1.ConditionTrue || c.Reason != "Accepted" {
		t.Fatalf("want Ready=True/Accepted, got %+v", c)
	}
}

func TestClusterReconcileHonest4xx(t *testing.T) {
	cp := testCP(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/sites"):
			json.NewEncoder(w).Encode([]map[string]string{{"id": "site-1", "name": "edge"}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/k8s/clusters"):
			json.NewEncoder(w).Encode([]map[string]string{})
		case r.Method == "POST":
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"code":"invalid_vip_range","message":"vip_range overlaps a site subnet"}}`))
		}
	})
	cr := &tunnexv1.TunnexCluster{
		ObjectMeta: managedMeta("ns", "c"),
		Spec:       tunnexv1.TunnexClusterSpec{Site: "edge", Name: "prod", VIPRange: "10.0.0.0/8", ServiceCIDR: "10.96.0.0/12", DNSZone: "k8s.acme.com"},
	}
	k := fakeK8s(t, cr)
	r := &TunnexClusterReconciler{Client: k, CP: cp}
	res, err := r.Reconcile(context.Background(), req("ns", "c"))
	if err != nil {
		t.Fatalf("a 4xx is not a controller error, got %v", err)
	}
	if res.RequeueAfter != clientErrRequeue {
		t.Fatalf("4xx should slow-requeue, got %+v", res)
	}
	var got tunnexv1.TunnexCluster
	k.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "c"}, &got)
	c := readyCond(t, got.Status.Conditions)
	if c == nil || c.Status != metav1.ConditionFalse || c.Reason != "invalid_vip_range" || !strings.Contains(c.Message, "overlaps a site subnet") {
		t.Fatalf("want honest Ready=False naming the CP code+message, got %+v", c)
	}
	if got.Status.ClusterID != "" {
		t.Fatalf("a rejected cluster must not carry a ClusterID, got %q", got.Status.ClusterID)
	}
}

func TestClusterReconcileKeepLastOn5xx(t *testing.T) {
	cp := testCP(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sites") {
			json.NewEncoder(w).Encode([]map[string]string{{"id": "site-1", "name": "edge"}})
			return
		}
		http.Error(w, "db down", 500) // the list call 5xxes
	})
	cr := &tunnexv1.TunnexCluster{
		ObjectMeta: managedMeta("ns", "c"),
		Spec:       tunnexv1.TunnexClusterSpec{Site: "edge", Name: "prod", VIPRange: "100.64.0.0/16", ServiceCIDR: "10.96.0.0/12", DNSZone: "k8s.acme.com"},
	}
	k := fakeK8s(t, cr)
	r := &TunnexClusterReconciler{Client: k, CP: cp}
	_, err := r.Reconcile(context.Background(), req("ns", "c"))
	if err == nil {
		t.Fatal("a 5xx must be returned as a controller error (backoff requeue)")
	}
	var got tunnexv1.TunnexCluster
	k.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "c"}, &got)
	if len(got.Status.Conditions) != 0 {
		t.Fatalf("keep-last: a 5xx must not touch status, got %+v", got.Status.Conditions)
	}
}

func TestClusterReconcileSiteNotFound(t *testing.T) {
	cp := testCP(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]string{}) // no sites
	})
	cr := &tunnexv1.TunnexCluster{
		ObjectMeta: managedMeta("ns", "c"),
		Spec:       tunnexv1.TunnexClusterSpec{Site: "ghost", Name: "prod", VIPRange: "100.64.0.0/16", ServiceCIDR: "10.96.0.0/12", DNSZone: "k8s.acme.com"},
	}
	k := fakeK8s(t, cr)
	r := &TunnexClusterReconciler{Client: k, CP: cp}
	if _, err := r.Reconcile(context.Background(), req("ns", "c")); err != nil {
		t.Fatal(err)
	}
	var got tunnexv1.TunnexCluster
	k.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "c"}, &got)
	if c := readyCond(t, got.Status.Conditions); c == nil || c.Reason != "site_not_found" {
		t.Fatalf("want Ready=False/site_not_found, got %+v", c)
	}
}

// ── service: ORDERING (waits for the cluster) then exposes ──────────────────────────────────────────────

func TestServiceWaitsForCluster(t *testing.T) {
	cluster := &tunnexv1.TunnexCluster{ObjectMeta: managedMeta("ns", "prod-cluster")} // status.ClusterID empty
	svc := &tunnexv1.TunnexExposedService{
		ObjectMeta: managedMeta("ns", "api"),
		Spec:       tunnexv1.TunnexExposedServiceSpec{Cluster: "prod-cluster", Namespace: "prod", Service: "api", Protocol: "tcp", Port: 80},
	}
	k := fakeK8s(t, cluster, svc)
	r := &TunnexExposedServiceReconciler{Client: k, CP: cpDown(t)} // must NOT reach the CP while waiting
	res, err := r.Reconcile(context.Background(), req("ns", "api"))
	if err != nil || res.RequeueAfter != requeueDependency {
		t.Fatalf("must wait on the unregistered cluster, got res=%+v err=%v", res, err)
	}
	var got tunnexv1.TunnexExposedService
	k.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "api"}, &got)
	if c := readyCond(t, got.Status.Conditions); c == nil || c.Reason != "WaitingForCluster" {
		t.Fatalf("want Ready=False/WaitingForCluster, got %+v", c)
	}
}

func TestServiceExposesWhenClusterReady(t *testing.T) {
	cluster := &tunnexv1.TunnexCluster{
		ObjectMeta: managedMeta("ns", "prod-cluster"),
		Status:     tunnexv1.TunnexClusterStatus{ClusterID: "clu-1"},
	}
	svc := &tunnexv1.TunnexExposedService{
		ObjectMeta: managedMeta("ns", "api"),
		Spec:       tunnexv1.TunnexExposedServiceSpec{Cluster: "prod-cluster", Namespace: "prod", Service: "api", Protocol: "tcp", Port: 80},
	}
	cp := testCP(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/k8s/services"):
			json.NewEncoder(w).Encode([]map[string]string{})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/services"):
			if !strings.Contains(r.URL.Path, "clu-1") {
				t.Errorf("expose must target the cluster's CP id, got %s", r.URL.Path)
			}
			json.NewEncoder(w).Encode(map[string]string{"id": "svc-1", "vip": "100.64.0.9", "fqdn": "api.prod.svc.prod.k8s.acme.com"})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	k := fakeK8s(t, cluster, svc)
	r := &TunnexExposedServiceReconciler{Client: k, CP: cp}
	if _, err := r.Reconcile(context.Background(), req("ns", "api")); err != nil {
		t.Fatal(err)
	}
	var got tunnexv1.TunnexExposedService
	k.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "api"}, &got)
	if got.Status.ServiceID != "svc-1" || got.Status.FQDN != "api.prod.svc.prod.k8s.acme.com" {
		t.Fatalf("derived status (id+copied FQDN) not mirrored: %+v", got.Status)
	}
	if c := readyCond(t, got.Status.Conditions); c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("want Ready=True, got %+v", c)
	}
}

// ── grant: ORDERING, subject resolution, honest edition_required ────────────────────────────────────────

func TestGrantWaitsForService(t *testing.T) {
	svc := &tunnexv1.TunnexExposedService{ObjectMeta: managedMeta("ns", "api")} // ServiceID empty
	grant := &tunnexv1.TunnexGrant{
		ObjectMeta: managedMeta("ns", "g"),
		Spec:       tunnexv1.TunnexGrantSpec{SubjectKind: "cidr", Subject: "10.0.0.0/24", Service: "api"},
	}
	k := fakeK8s(t, svc, grant)
	r := &TunnexGrantReconciler{Client: k, CP: cpDown(t)}
	res, err := r.Reconcile(context.Background(), req("ns", "g"))
	if err != nil || res.RequeueAfter != requeueDependency {
		t.Fatalf("must wait on the unexposed service, got res=%+v err=%v", res, err)
	}
	var got tunnexv1.TunnexGrant
	k.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "g"}, &got)
	if c := readyCond(t, got.Status.Conditions); c == nil || c.Reason != "WaitingForService" {
		t.Fatalf("want Ready=False/WaitingForService, got %+v", c)
	}
}

func TestGrantEditionRequiredHonest(t *testing.T) {
	svc := &tunnexv1.TunnexExposedService{
		ObjectMeta: managedMeta("ns", "api"),
		Status:     tunnexv1.TunnexExposedServiceStatus{ServiceID: "svc-1"},
	}
	grant := &tunnexv1.TunnexGrant{
		ObjectMeta: managedMeta("ns", "g"),
		Spec:       tunnexv1.TunnexGrantSpec{SubjectKind: "cidr", Subject: "10.0.0.0/24", Service: "api"},
	}
	cp := testCP(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"error":{"code":"edition_required","message":"Zero Trust governance requires the enterprise edition"}}`))
	})
	k := fakeK8s(t, svc, grant)
	r := &TunnexGrantReconciler{Client: k, CP: cp}
	res, err := r.Reconcile(context.Background(), req("ns", "g"))
	if err != nil {
		t.Fatalf("edition_required is a 4xx, not a controller error, got %v", err)
	}
	if res.RequeueAfter != clientErrRequeue {
		t.Fatalf("want slow requeue, got %+v", res)
	}
	var got tunnexv1.TunnexGrant
	k.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "g"}, &got)
	c := readyCond(t, got.Status.Conditions)
	if c == nil || c.Status != metav1.ConditionFalse || c.Reason != "edition_required" {
		t.Fatalf("want honest Ready=False/edition_required (never a silent no-op), got %+v", c)
	}
	if got.Status.RuleID != "" {
		t.Fatalf("a refused grant must not carry a RuleID, got %q", got.Status.RuleID)
	}
}

func TestGrantResolvesUserSubjectAndCreates(t *testing.T) {
	svc := &tunnexv1.TunnexExposedService{
		ObjectMeta: managedMeta("ns", "api"),
		Status:     tunnexv1.TunnexExposedServiceStatus{ServiceID: "svc-1"},
	}
	grant := &tunnexv1.TunnexGrant{
		ObjectMeta: managedMeta("ns", "g"),
		Spec:       tunnexv1.TunnexGrantSpec{SubjectKind: "user", Subject: "alice@acme.com", Service: "api"},
	}
	var body map[string]any
	cp := testCP(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/members"):
			json.NewEncoder(w).Encode([]map[string]string{{"user_id": "u-1", "email": "alice@acme.com"}})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/policies"):
			b, _ := io.ReadAll(r.Body)
			json.Unmarshal(b, &body)
			json.NewEncoder(w).Encode(map[string]string{"id": "rule-1"})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	k := fakeK8s(t, svc, grant)
	r := &TunnexGrantReconciler{Client: k, CP: cp}
	if _, err := r.Reconcile(context.Background(), req("ns", "g")); err != nil {
		t.Fatal(err)
	}
	if body["src_kind"] != "user" || body["src_user_id"] != "u-1" || body["dst_kind"] != "k8s_service" || body["dst_k8s_service_id"] != "svc-1" {
		t.Fatalf("resolved grant body wrong: %+v", body)
	}
	var got tunnexv1.TunnexGrant
	k.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "g"}, &got)
	if got.Status.RuleID != "rule-1" {
		t.Fatalf("want RuleID rule-1, got %q", got.Status.RuleID)
	}
	if c := readyCond(t, got.Status.Conditions); c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("want Ready=True, got %+v", c)
	}
}

// ── Slice 4: finalizer delete through the AUDITED verb, CR named as the cause ───────────────────────────

func TestClusterFinalizeDeletesWithCause(t *testing.T) {
	var method, path, cause string
	cp := testCP(t, func(w http.ResponseWriter, r *http.Request) {
		method, path, cause = r.Method, r.URL.Path, r.Header.Get("X-Tunnex-Cause")
		w.WriteHeader(204)
	})
	cr := &tunnexv1.TunnexCluster{ObjectMeta: managedMeta("ns", "c"), Status: tunnexv1.TunnexClusterStatus{ClusterID: "clu-1"}}
	k := fakeK8s(t, cr)
	if err := k.Delete(context.Background(), cr); err != nil { // finalizer present → object lingers w/ deletionTimestamp
		t.Fatal(err)
	}
	r := &TunnexClusterReconciler{Client: k, CP: cp}
	if _, err := r.Reconcile(context.Background(), req("ns", "c")); err != nil {
		t.Fatal(err)
	}
	if method != "DELETE" || !strings.Contains(path, "clu-1") {
		t.Fatalf("finalize must DELETE the CP cluster, got %s %s", method, path)
	}
	if cause != "tunnexcluster:ns/c" {
		t.Fatalf("the audit cause must NAME THE CR (D2 cond 2), got %q", cause)
	}
	var got tunnexv1.TunnexCluster
	if err := k.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "c"}, &got); !apierrors.IsNotFound(err) {
		t.Fatalf("finalizer must be released (CR gone) after teardown, got err=%v finalizers=%v", err, got.Finalizers)
	}
}

func TestGrantFinalizeRevokesWithCause(t *testing.T) {
	var method, path, cause string
	cp := testCP(t, func(w http.ResponseWriter, r *http.Request) {
		method, path, cause = r.Method, r.URL.Path, r.Header.Get("X-Tunnex-Cause")
		w.WriteHeader(204)
	})
	cr := &tunnexv1.TunnexGrant{ObjectMeta: managedMeta("ns", "g"), Status: tunnexv1.TunnexGrantStatus{RuleID: "rule-1"}}
	k := fakeK8s(t, cr)
	if err := k.Delete(context.Background(), cr); err != nil {
		t.Fatal(err)
	}
	r := &TunnexGrantReconciler{Client: k, CP: cp}
	if _, err := r.Reconcile(context.Background(), req("ns", "g")); err != nil {
		t.Fatal(err)
	}
	if method != "DELETE" || !strings.Contains(path, "rule-1") || cause != "tunnexgrant:ns/g" {
		t.Fatalf("finalize must DELETE the rule naming the CR, got %s %s cause=%q", method, path, cause)
	}
}

// A CP 404 on delete is idempotent success — the object is already gone (a retried finalizer / a cascade
// that already swept it converges instead of wedging the CR forever).
func TestServiceFinalizeIdempotentOn404(t *testing.T) {
	cp := testCP(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"error":{"code":"service_not_found","message":"gone"}}`))
	})
	cr := &tunnexv1.TunnexExposedService{ObjectMeta: managedMeta("ns", "api"), Status: tunnexv1.TunnexExposedServiceStatus{ServiceID: "svc-1"}}
	k := fakeK8s(t, cr)
	if err := k.Delete(context.Background(), cr); err != nil {
		t.Fatal(err)
	}
	r := &TunnexExposedServiceReconciler{Client: k, CP: cp}
	if _, err := r.Reconcile(context.Background(), req("ns", "api")); err != nil {
		t.Fatalf("a 404 on delete must be idempotent success, got %v", err)
	}
	var got tunnexv1.TunnexExposedService
	if err := k.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "api"}, &got); !apierrors.IsNotFound(err) {
		t.Fatalf("CR must be gone even when the CP object was already absent, got err=%v", err)
	}
}

// ── Slice 4: drift surfaces in status (D2 cond 3) ───────────────────────────────────────────────────────

func TestClusterDriftRecreatesAndSurfaces(t *testing.T) {
	cp := testCP(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/sites"):
			json.NewEncoder(w).Encode([]map[string]string{{"id": "site-1", "name": "edge"}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/k8s/clusters"):
			json.NewEncoder(w).Encode([]map[string]string{}) // GONE — deleted out-of-band
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/k8s/clusters"):
			json.NewEncoder(w).Encode(map[string]string{"id": "clu-2", "dns_vip": "100.64.0.53"})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	cr := &tunnexv1.TunnexCluster{
		ObjectMeta: managedMeta("ns", "c"),
		Spec:       tunnexv1.TunnexClusterSpec{Site: "edge", Name: "prod", VIPRange: "100.64.0.0/16", ServiceCIDR: "10.96.0.0/12", DNSZone: "k8s.acme.com"},
		Status:     tunnexv1.TunnexClusterStatus{ClusterID: "stale-1"}, // we believed it existed
	}
	k := fakeK8s(t, cr)
	r := &TunnexClusterReconciler{Client: k, CP: cp}
	if _, err := r.Reconcile(context.Background(), req("ns", "c")); err != nil {
		t.Fatal(err)
	}
	var got tunnexv1.TunnexCluster
	k.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "c"}, &got)
	if got.Status.ClusterID != "clu-2" {
		t.Fatalf("drift must recreate from the CR (new id), got %q", got.Status.ClusterID)
	}
	d := apimeta.FindStatusCondition(got.Status.Conditions, condDrift)
	if d == nil || d.Status != metav1.ConditionTrue || d.Reason != "RecreatedFromCR" {
		t.Fatalf("drift must SURFACE in status (D2 cond 3), got %+v", d)
	}
}
