# Tunnex — poora product walkthrough (Hinglish)

**Ye kya hai.** Ek continuous journey — wahi order jisme ek customer product ko actually milta hai. Feature
list nahi, **sequence** hai. Har step pichhle step ke ऊpar khada hai.

**Har step mein teen cheezein:**
1. **Customer kya click karta hai** — exact screen, exact control
2. **Technically kya hota hai** — control plane kya karta hai, data plane tak kya pahunchta hai
3. ⛔ **Kyun karega, aur kya milega** — outcome, feature nahi

**Verification.** Har claim code se ya chalte hue product se, 2026-08-07. Jo verify nahi hua, wo
**⚠ FOUNDER MUST TRY** likha hai. ⛔ **Jo gap hai wo usi step pe likha hai** — kyunki ek walkthrough jo gap
ke ऊpar se poora dikhta hai, wo gap se zyada khatarnak hai. Usko demo kar diya jata hai.

**Install skip.** Founder repo clone karega, `docker compose up`. Ye document ek **chalte hue control
plane** se shuru hota hai.

---

## ⛔ Pehle do axes samajh lo — warna poora document confusing lagega

Tunnex mein **do alag cheezein** feature ko rok sakti hain, aur logo inhe mila dete hain:

**1. EDITION (build tag)** — `open` binary vs `enterprise` binary. Refusal: `403 edition_required`.
Code se verified — ye handlers gate karte hain:
`sso_handlers.go` · `policy_handlers.go` (Zero Trust) · `device_health_handlers.go` (posture) ·
`mfa_enforce_handlers.go` · `device_posture_handlers.go` (approval) · `access_log_handlers.go`.

**2. LICENCE TIER** — Community / Trial / Starter / Growth / Scale. Ye **ceilings** aur **kuch features**
decide karta hai. Refusal: `gateway_limit_reached`, `org_limit_reached`.

⭐ **Aur licence tiers ki asli shakal ye hai (`licence/entitlements.go` se, seedha):**

| Tier | Gateways | Orgs | SSO | IdP sync |
|---|---|---|---|---|
| **Community** | **1** | **1** | ❌ | ❌ |
| Trial | 2 | 1 | ✅ | ✅ |
| Starter | 5 | ∞ | ✅ | ✅ |
| Growth | 20 | ∞ | ✅ | ✅ |
| Scale | ∞ | ∞ | ✅ | ✅ |

⛔ **Community deliberately generous hai, aur ye strategy hai — accident nahi.** Code ka apna comment:
poora Zero Trust engine, AI agents, Kubernetes, OpenVPN, posture, approval, MFA enforcement, Access Events
aur full-retention audit log — **sab free**. Paid sirf: multi-gateway, multi-org, SSO, IdP sync.

> *"The moat is Community's generosity, not Enterprise's length — a thinner Community loses to NetBird,
> whose self-hosted edition is free with unlimited users."*

⚠ **Iska matlab ye hai:** neeche jo bhi "enterprise" likha hai wo **edition** ki baat hai (kaunsa binary
chal raha hai), **band** ki nahi. Community band pe enterprise binary chalao to Zero Trust free milta hai.

**Ab customer ke paas kuch nahi hai. Pehla kaam — andar ghusna.**

---

# PART 1 — SETUP

## 1.1 CP admin ka pehla login

**Click.** `http://<address>/` kholo. Email `admin@tunnex.local`, password wo jo first-boot pe terminal pe
chhapa tha.

**Technically.** `bootstrap.EnsureAdmin` first boot pe ek baar chala. Condition: *"is deployment mein kabhi
koi user tha?"* — aur ye **soft-deleted rows bhi count karta hai**, isliye saare accounts delete karke bhi
koi admin-minting dobara nahi khol sakta. Password generate hua, argon2id se hash hua, aur **plaintext sirf
us banner mein** tha. Account pe `users.cp_admin` flag hai.

⛔ **Banner API container ke stdout pe jata hai, log line pe nahi** — code ka comment kehta hai ki JSON log
line "correct, greppable, invisible" thi, isliye framed banner banaya gaya.

**Kyun.** Ye ekmatra rasta hai jo internet se race nahi karta. Baaki sab — signup, invitation, SSO — ya to
band ho jate hain ya kisi andar wale bande pe depend karte hain.

⛔ **Recovery hai hi nahi.** Sign-in se pehle credential kho gaya to documented jawab hai
`docker compose down -v` — deployment khatam karo, dobara shuru karo. Banner khud ye likhta hai.

⚠ **Aur ek window hai jo abhi khuli hai.** `signup_closed` ka gate `CountOrganizationsEver > 0` pe hai
(`auth_handlers.go:48`) — fresh install pe wo **zero** hai. Matlab bootstrap admin ke hote hue bhi
**signup khula hai**, aur jo bhi pehle signup karke pehla org bana lega wo deployment ka malik ban jayega.
Window pehla org banate hi band ho jati hai. ⛔ **Aur `/auth/signup` pe koi rate limit nahi hai** — ye code
mein khud registered hai (`tenancy/service.go`).

**Ab customer ke paas: ek admin session. Aage: apna password set karna padega.**

---

## 1.2 Forced password change

**Click.** Seedha change-password screen pe pahunchte ho. Kahin aur nahi ja sakte.

**Technically.** Bootstrap password us pal kaam karna band kar deta hai jab tum apna set karte ho. Ye ek
real route guard hai, suggestion nahi.

**Kyun.** Pehla credential ek terminal pe chhapa tha aur shayad log aggregator mein bhi chala gaya. Uski
umar minute mein honi chahiye, din mein nahi.

⚠ **FOUNDER MUST TRY:** confirm karo ki wall actually rokti hai, aur purana password baad mein refuse hota
hai.

**Ab customer ke paas: apna password. Aage: pehla organization.**

---

## 1.3 Pehla organization

**Click.** `/create-org` pe guide hote ho. Naam do.

**Technically.** `checkMayCreateOrg` isliye allow karta hai kyunki `CountOrganizationsEver == 0`
(bootstrap) — ya baad mein isliye ki tumhare paas `cp_admin` hai. Org banate hi **signup permanently band**
ho jata hai: `/auth/signup` ab `403 signup_closed` deta hai, human reason ke saath — *"accounts are created
by invitation — ask an administrator to invite you."*

**Kyun.** Organization hi tenancy boundary hai. Har device, gateway, policy, site aur audit row isi ke andar
scoped hai.

⭐ **Design self-closing hai** — koi setting nahi, kuch galat configure karne ko nahi, kuch yaad rakhne ko
nahi. Jo condition window kholti hai, wo window use karne se hi khatam ho jati hai.

⚠ **Org A ka member org B nahi bana sakta.** Identity model mein authority `map[orgID]role` hai — org-keyed
by construction — isliye naya org banane ka licence sirf `cp_admin` deta hai. Baaki sabko **403 with
reason**, 404 nahi — taki unhe pata chale ki invitation hi rasta hai.

**Ab customer ke paas: ek org, jisme wo akela hai. Aage: doosre logon ko bulana.**

---

## 1.4 Pehla user invite karo

**Click.** Users & Roles → *Invite by email* → address, role, **Send invite**.

**Technically.**
- Row banti hai aur audit event likha jata hai — **ek hi transaction mein**.
- Email best-effort jata hai. ⭐ **Delivery fail ho to bhi invitation zinda rehta hai** — row real hai, link
  valid hai, to mail down hone par wo ek cheez destroy nahi karni chahiye jo tum haath se de sakte ho.
- API **202** deta hai, `invite_token` (raw accept link) aur `delivered: true|false` ke saath.
- Fail pe message: *"Invitation created — BUT THE EMAIL COULD NOT BE SENT. Copy the link below and send it
  to them yourself."*
- Screen ek one-time modal dikhata hai copyable link ke saath — chahe mail gaya ho ya nahi.

**Kyun.** Setup ke baad **invitation hi ekmatra rasta hai** kisi ke andar aane ka. Signup band hai, aur bina
membership wala account kahin nahi pahunchta.

### Invitation email kaisa dikhta hai (wire pe verified, 2026-08-07)

Branded: dark card, Tunnex wordmark, **Accept the invitation** button, poora URL uske neeche, aur footer
*"Tunnex · Connect everything. Trust nothing."*

⭐ **Logo message ke andar embedded hai (`cid:`), fetch nahi hota.** Third-party MIME parser se measured:
`multipart/related [ multipart/alternative [ text/plain, text/html ], image/png ]`, aur decoded PNG source
se **sha256-identical**. **Mail khulne pe kuch request nahi jati** — na humein phone-home, na tumhare apne
logs mein open-tracking hit, aur wo clients mein bhi render hota hai jo remote images block karte hain.

**Har message mein working plaintext body hai** poore link ke saath — screen readers, text clients, aur wo
log jo email mein button pe bharosa nahi karte.

### ⛔ Is step ke named gaps

- **Screen bata nahi sakta ki mail gaya ya nahi.** 202 mein `delivered` aata hai, web UI use padhti hi
  nahi — link modal success aur failure pe bilkul same dikhta hai. Log dekho.
- **Resend koi token nahi deta.** `ResendInvitation` sirf message deta hai, jabki wo khud kehta hai
  "copy the link from the invitations list". Bina SMTP wale box pe resend kiya hua invitation
  **unrecoverable** hai.
- **Header injection unfixed.** `buildRFC822` `To` aur `Subject` ko headers mein bina CRLF stripping ke
  daalta hai — CodeQL ka `go/email-injection`. Pre-existing, aur SMTP on hone ke baad zyada reachable.

### Mail actually gaya ya nahi, ye kaise pata karo

```bash
docker compose logs -f api | grep -E "mail_destination|email_accepted_by_provider|invite_email_failed"
```

- Boot pe `mail_destination` batata hai mail kahan jata hai — `mail.spacemail.com:587`, ya ye ki mail
  disabled hai aur kaunsa variable use theek karta hai.
- `email_accepted_by_provider` = server ne message **accept** kiya. Ye inbox delivery nahi hai — SPF/DKIM
  aur recipient ke filters iske baad kaam karte hain, aur uska jawab provider ke outbound log ke paas hai.
- `invite_email_failed` provider ka verbatim error leke aata hai.

⚠ **Port 587, 465 nahi.** Go ka `net/smtp` plaintext dial karke STARTTLS se upgrade karta hai; uske paas
**implicit-TLS ka rasta hai hi nahi**, isliye SMTPS port hang ya error karega. Ye standard library ki
property hai — configuration se theek nahi hota.

⚠ **`MAIL_DEV_LOG` kabhi deployment pe set mat karna** — wo message bodies tee karta hai, aur wo bodies
kaam karne wale links hain.

**Ab customer ke paas: ek bheja hua invitation. Aage: banda andar aaye.**

---

## 1.5 Invited user apna password set karta hai aur sign in karta hai

**Click.** Link kholta hai → `/accept-invite?token=…` → *Your name* aur *Password* bharta hai → **Accept
invitation** → **"You're in"** dekhta hai → `/login` pe jata hai → sign in karta hai.

**Technically.** Accept endpoint `security: []` hai (public) aur **account khud banata hai** — invite hona
hi admission hai, account sirf credential hai. Email verified bhi mark hota hai, kyunki us address pe bheje
gaye link se aana hi proof hai. Token single-use aur expiring hai. Page URL se token strip kar deta hai.
⭐ **Accept karne se session nahi banta** — deliberately, kyunki link admin ko dikh chuka hai.

**Kyun.** Banda apna password khud set karta hai; koi aur usse chhoota hi nahi. Aur jis admin ne modal se
link copy kiya tha, wo us link se account mein ghus nahi sakta.

### ⛔ Wo assertion jo asli hai

Sign in ke baad wo **Overview** pe hona chahiye, URL `/dashboard` pe khatam.

**"Invitation required" card pe nahi. `/create-org` pe nahi.** Yahi wo failure hai jiske liye poori chain
dobara banayi gayi, aur `e2e/tests/invitation.spec.ts` ab ek real invitation end-to-end chalata hai aur
theek isi pe assert karta hai — bina mocks ke, token 202 body se.

⚠ **FOUNDER MUST TRY — aur yahi wo leg hai jo real mail ke saath kabhi nahi chala.** Delivery proven hai
(2026-08-07 ko ek real invitation `support@tunnex.io` se Spacemail ke through Gmail inbox mein pahuncha).
Accept → sign-in → lands-in-org sirf local stack pe e2e spec se proven hai.

**Mail problem aur routing problem mein farq:**
- Log clean, kuch nahi aaya → **mail**. Spam dekho, phir provider ka outbound log.
- Mail aaya par link mein **galat host** → `APP_BASE_URL`. Link **server-side** banta hai, browser se nahi.
- Link chala, sign-in chala, par "Invitation required" pe utra → **na mail na routing. Wahi loop hai.**

**Ab customer ke paas: do log, ek org. Setup poora. Aage: identity — log kaise aayenge.**

---

# PART 2 — ACCESS (identity)

## 2.1 Local auth

**Click.** `/login` — email + password. Bhool gaye to `/forgot-password`.

**Technically.** Password argon2id se hashed. Reset ek single-use expiring token bhejta hai. Sessions Redis
mein hain (`SessionID` cookie-only minting), bearer ≡ cookie, aur **no-oracle 401s** — expired session bhi
client-local `expires_at` se detect hota hai taki server "ye email exist karta hai" na bata de.

**Kyun.** Har deployment ko SSO nahi chahiye. Chhoti team ke liye local auth hi kaafi hai, aur ye
Community band pe free hai.

⚠ **Reset aur verification dono mail pe depend karte hain.** Bina SMTP ke inka koi self-service rasta nahi
hai — jo section 1.4 ka gap ek layer neeche hai.

**Ab: log password se aa sakte hain. Aage: company ke IdP se.**

---

## 2.2 SSO — Google aur Microsoft Entra

**Click.** Org Settings → SSO → provider chuno, client ID/secret daalo.
API: `getSsoConfig` / `setSsoConfig` / `startSsoLogin` / `ssoCallback`.

**Technically.** `/meta` unauthenticated batata hai kaunse providers configured hain — login page ko ye
jaanna hota hai isse pehle ki koi sign in kare. Callback pe identity resolve hoti hai aur, agar domain
capture on hai, JIT provisioning hota hai.

**Kyun.** Enterprise buyer ke liye "kya ye humare liye kaam karega" ka matlab hi SSO hai. Offboarding IdP
se hota hai, Tunnex se nahi.

⛔ **Do gates, dono alag:**
- **Edition:** `sso_handlers.go` → `403 edition_required` — *"SSO is a Tunnex Enterprise feature"* (open
  binary pe).
- **Band:** Community pe SSO **nahi** hai. Trial se upar sab bands pe hai.

⭐ **Aur ek design decision jo samajhne layak hai:** **SSO LOGIN kabhi gated nahi hota.** Code ka comment —
*"a licence state must never lock a human out"*. Trial lapse hone pe jo band hota hai wo sirf wo hai jo
**kisi naye ko onboard** karta hai: JIT provisioning aur domain-capture auto-join. Jo log trial ke dauran
exist karte the wo andar hi rehte hain.

⚠ **FOUNDER MUST TRY:** real Google aur real Entra tenant ke saath. Maine sirf handlers aur gates padhe
hain, live SSO round-trip nahi chalaya.

**Ab: log company identity se aa sakte hain. Aage: groups automatically sync karna.**

---

## 2.3 Directory sync (Entra groups)

**Click.** Org Settings → Directory sync. API: `putIdpSyncConfig` · `getIdpSyncHealth` · `triggerIdpSync` ·
`mapIdpGroup` · `unmapIdpGroup`.

**Technically.** Directory ke groups Tunnex groups pe map hote hain, aur **wo groups policy subjects ban
jate hain**. Reconciler **fail-static** hai — sync fail ho to purani state rehti hai, khali nahi hoti.
`getIdpSyncHealth` batati hai sync zinda hai ya nahi.

**Kyun.** Access "kaun kis group mein hai" se chalta hai, aur wo sach HR/IT ke directory mein hai. Manual
copy karne ka matlab hai drift, aur drift ka matlab hai wo log jo nikal gaye par access rakhte hain.

⛔ **Deprovision fail-open ek pura cluster tha jo review mein pakda aur fix hua** (delete-sweep wired) —
matlab directory se hataye gaye bande ka access hatna chahiye.

⚠ **Sirf Entra.** Google directory sync aur SCIM **registered-but-unbuilt** (S7.5.2b), `deleted_in_directory`
cause ke saath.

⛔ **Edition-gated aur band-gated dono.** Community pe nahi.

**Ab: identity aur groups automatic hain. Aage: apne domain ke log khud aa jaayein.**

---

## 2.4 Domain capture

**Click.** Org Settings → domain claim. API: `createDomainClaim` · `verifyDomainClaim`.

**Technically.** Domain claim karo, DNS se verify karo. Uske baad us domain ka koi bhi SSO user
**automatically** us org mein aa jata hai — invitation ke bina. Ye ek **admission path** hai, aur code ne
ise deliberately signup se independent rakha hai: `TestAdmissionPathsDoNotDependOnSignup` isko prove karta
hai.

**Kyun.** 200 logon ki company ko 200 invitations nahi bhejne — `@acme.com` wala koi bhi andar aa jaye.

⚠ **DNS verification zaroori hai** — warna koi bhi kisi ka domain claim karke uske SSO users ko apne org
mein khींch sakta tha.

⛔ **Trial lapse hone pe domain-capture auto-join band ho jata hai** (SSO login nahi). Upar 2.2 dekho.

**Ab: log apne aap andar aa rahe hain. Aage: unka sign-in mazboot karna.**

---

## 2.5 MFA (TOTP)

**Click.** User apne liye: Settings → MFA → **Enroll** → QR scan → 6 digits confirm → recovery codes
dikhte hain **ek baar**. Admin ke liye: Org Settings → MFA enforcement.
API: `mfaEnrollStart` · `mfaEnrollConfirm` · `mfaVerify` · `mfaDisenroll` · `getMfaEnforce` /
`setMfaEnforce` · `adminResetMfa`.

**Technically.** Enrolment **open** hai (koi bhi apne liye MFA laga sakta hai), **enforcement**
enterprise hai. Login pe MFA challenge **session nahi** hai — challenge aur session alag primitives hain,
aur "kis method se login hua" ek first-class principal property hai.

**Kyun.** Password chori ho jaata hai. MFA hi wo ek control hai jo credential stuffing ko rokta hai — aur
open enrolment ka matlab hai ki security-conscious log turant laga sakte hain, admin ke intezaar ke bina.

⭐ **Unlock-then-opt-in, founder-directed.** Enterprise edition MFA enforcement ko **available** karta hai;
**on nahi karta**. Org-level opt-in, default OFF.

⛔ **`adminResetMfa` ek security event hai aur wo mail bhejta hai** — "An administrator reset the two-factor
authentication (MFA) on your Tunnex account… contact your administrator immediately." ⭐ **Us mail mein koi
link ya button nahi hai, deliberately** — kyunki "secure your account" wala button hi wo shakal hai jo
iski phishing copy leti hai.

⚠ **SSO-exempt wire proof DEFERRED** (named trigger ke saath). Matlab: SSO se aane wale users MFA
enforcement se exempt hone chahiye, aur wo **wire pe** prove nahi hua — sirf unit tests se.

⚠ **WF-2 break-glass registered** — agar MFA required hai aur admin apna device kho de, uska rasta abhi
poora nahi hai.

**Ab: identity poori hai — log aa sakte hain, safely. Aage: unhe network pe laana.**

---

# PART 3 — NETWORK

## 3.1 Pehla gateway enrol karo

**Click.** Gateways → **Enroll gateway** → naam do → **Generate join token**. Screen ek **poora command**
deta hai. Usko apne server pe chalao.
API: `issueJoinToken` · `enrollAgent`.

**Technically.** Token single-use aur expiring hai, `node_join_tokens` mein hashed. Agent CSR bhejta hai;
control plane use sign karta hai (48h cert). Uske baad **mTLS channel** — agent apne cert se authenticate
karta hai, `desired state` maangta hai, aur reconcile loop chalata hai. ⛔ **Control plane WireGuard ko
kabhi chhoota nahi** — wo poora node-agent ka kaam hai (`wgctrl`).

⭐ **Token PEEK hota hai, consume nahi — jab tak sab checks pass na ho.** `PeekJoinToken` isliye bana kyunki
`node_name_mismatch` ne ek hi session mein do token jala diye the: operator UI mein naam deta hai, agent
container hostname se register karta hai, dono match nahi karte — 5 second ka fix, par token gaya, aur agli
koshish `invalid_join_token` deti thi, jo bilkul alag problem describe karta hai.

**Kyun.** Gateway hi wo cheez hai jo actually traffic carry karti hai. Iske bina Tunnex ek dashboard hai.

⛔ **Gateway ko public reachability ya port-forward chahiye. Tunnex koi relay fleet nahi chalata.** NAT ke
peeche bina forwarded port ke gateway control plane tak pahunch to sakta hai, par peers use dial nahi kar
sakte — matlab wo site transit nahi kar sakta. Ye product ki screen pe likha hai.

⛔ **Community ceiling = 1 gateway.** Doosra chahiye to Trial ya paid band.

**Ab: ek live gateway. Aage: log usse connect karein.**

---

## 3.2 Devices — config, QR, desktop client

**Click.** Devices → **Add device** → naam, platform. Do raste:
- **Managed** — Tunnex desktop client (Electron) use karta hai
- **Static export** — config file ya QR, mobile WireGuard app ke liye

API: `createDevice` · `listDevices` · `revokeDevice` · `removeDevice`.

**Technically.** Device ko org pool (`organizations.pool_cidr`) se ek `/32` milta hai. Config mein gateway
ka **endpoint aur public key** bake hote hain. Private key ya to client generate karta hai ya server —
server wala **one-time** dikhta hai, dobara nahi.

⛔ **Identity ↔ credential binding:** device credential sirf apne owner user ke liye valid hai. Koi floating
credentials nahi. Revocation ek **full sweep** hai — peer slot + pool address + telemetry.

**Kyun.** Yahi wo pal hai jab customer ko pehli baar kaam karta hua tunnel dikhta hai.

⭐ **`needs_reexport` batata hai kab config purana ho gaya**, teen causes ke saath:
1. **Routes** — baked ranges org ke current routed ranges se alag (sirf static)
2. **Address** — config ka address device ke current address se alag (har mode)
3. **Gateway** — config jis gateway ko naam deta hai wo ab uska gateway nahi

⛔ **Cause 3 ka managed-half abhi (S12.14) hi bana.** "Managed devices khud re-home kar lete hain" sirf
**hub-set members** ke liye sach tha — baaki gateways pe client apna baked endpoint rakh leta hai. Matlab
ek managed device jo aam gateway pe move hua, wo **database mein moved aur wire pe toota** tha, aur har
surface saaf dikhti thi.

⚠ **FOUNDER MUST TRY:** QR se mobile WireGuard app, aur desktop client ka connect. Maine handlers padhe
hain, live connect nahi kiya.

**Ab: ek connected device. Aage: saara traffic tunnel se bhejna.**

---

## 3.3 Full-tunnel + kill-switch

**Click.** Desktop client → split-tunnel toggle off (full-tunnel on).

**Technically.** Full-tunnel pe default route tunnel mein jata hai, aur gateway egress NAT karta hai.
**Kill-switch** OS level pe hai — macOS pe `pf`, Windows pe WFP — taki tunnel gire to traffic leak na ho.
Split↔full switch pe credential **re-mint** hota hai full-sweep revoke ke saath.

⛔ **Dono kill-switches wire pe proven hain** (memory se nahi — ye repo ka record hai): macOS `kill -9`
pcap aur Windows `taskkill /F` pcap, dono mein **zero cleartext v4+v6** helper process marne ke baad.
Windows pe persistence isliye chahiye thi kyunki wireguard-windows `FWPM_SESSION_FLAG_DYNAMIC` use karta
hai — process marte hi filters auto-delete ho jate the.

**Kyun.** Ye wo cheez hai jo "VPN" ko "compliance-grade VPN" banati hai. Kill-switch ke bina laptop band
hote hi leak ho sakta hai.

⚠ **`apps/helper/internal/wfp/` ek PINNED, DIVERGED fork hai** wireguard/windows ka. Upstream bump pe
re-diff aur re-apply karna **obligation** hai — warna upstream ka koi filter-set security patch miss ho
jayega.

⚠ **Windows full-tunnel re-homing abhi open item hai** (S8.6b-win-carveout). Split-tunnel pe kaam karta hai.

⚠ **Code signing DEFERRED.** macOS unsigned `.pkg` (Gatekeeper warning), Windows unsigned `.exe`
(SmartScreen). Trigger: public beta ya pehla outside-circle distribution. Windows EV additionally legal
entity formation pe.

**Ab: full-tunnel VPN chal raha hai. Aage: office ke subnets tak pahunchna.**

---

## 3.4 Routed ranges

**Click.** Routed ranges → range add karo. API: `listRoutedRanges` · `routeLAN`.

**Technically.** Approved ranges gateway ke routes mein jate hain aur clients tak **routed-ranges poll** se
pahunchte hain — bina tunnel bounce kiye, live apply. Static exports poll nahi karte, isliye unka
`needs_reexport` route-cause pe fire karta hai.

**Kyun.** Log office ke `10.0.0.0/16` tak pahunchna chahte hain, sirf ek dusre tak nahi. Yahi VPN ko LAN
extension banata hai.

**Ab: ek site poori. Aage: doosri site.**

---

# PART 4 — MULTI-SITE

## 4.1 Doosra gateway aur sites

**Click.** Sites → **Register site** → gateway ko site se bind karo.
API: `registerSite` · `bindSiteNode` · `unbindSiteNode` · `listSiteSubnets` · `addSiteSubnet` ·
`approveSiteSubnet`.

**Technically.** Site ek **first-class entity** hai. Subnets site ke neeche aate hain aur **approval**
maangte hain (`listPendingSiteSubnets` / `approveSiteSubnet`). Ek hi **disjointness validator** dono seams
pe chalta hai taki overlapping CIDRs na aayein.

⛔ **Community ceiling 1 gateway hai — multi-site ke liye Trial ya paid band chahiye.**

**Ab: do sites. Aage: unke beech traffic.**

## 4.2 Site-to-site aur hub-and-spoke

**Technically.** `src_kind='site'` policy subject ban jata hai. Topology **hub-and-spoke** hai: spoke sirf
hub se peer karta hai (AllowedIPs = saare remote subnets), hub har spoke se peer karta hai, aur hub beech
mein forward karta hai. Routes proto-static, metric 8021, **full-sweep**. MSS clamp lagta hai.

⭐ **Gateway zero-touch hai.** Cross-cloud walk mein Docker ka `filter FORWARD DROP` forward nigal raha
tha — ab agent khud ek **Routes-scoped DOCKER-USER accept** rule own karta hai (idempotent,
Docker-conditional). Policy phir bhi enforce hoti hai.

**Kyun.** AWS ka VPC aur Azure ka VNet ek dusre se baat karein, bina site-to-site VPN appliance ke.

⭐ **Cross-cloud proven:** AWS Sydney ↔ Azure West US, ~138ms, live walk.

## 4.3 Cross-site DNS

**Click.** Sites → DNS forwards. API: `listSiteDNSForwards` · `setSiteDNSForward` · `removeSiteDNSForward`.

**Technically.** Gateway pe DNS forwarder chalta hai; macOS pe resolver helper. `dns_forwarding` deliberately
**out-of-hash** hai.

**Kyun.** `db.internal` remote site pe resolve ho — IP yaad rakhne se log VPN chhod dete hain.

⭐ Route53 private zone Azure se in-VPC resolver hop ke through kaam karta hai — walk mein verified.

## 4.4 HA failover

**Click.** Gateways → hub priority pin karo. API: `setHubPriority` · `getHubSet`.

**Technically.** Org-level hub set elect hota hai: `hub_priority` (admin pin) > health (fresh-before-stale)
> id. `members[0]` active transit hub hai. **WF-A hub promotion:** chalta hua device dial channel se peer
**swap** karta hai — endpoint update nahi, identity (`node_id`) wahin rehti hai.

**Kyun.** Hub gir gaya to poora site mesh gir jata hai. Standby chahiye.

⚠ **Failover hysteresis persistence beta-blocking registered hai** — abhi nahi bana.

**Ab: mesh network. Aage: kaun kya reach kar sakta hai, ye decide karna.**

---

# PART 5 — ZERO TRUST

**Click.** Access Policies. API: `getZeroTrustMode` · `setZeroTrustMode` · `listResources` ·
`createResource` · `listPolicyRules` · `createPolicyRule` · `setPolicyRuleEnabled` · `extendGrant` ·
`listGroups` · `addGroupMember`.

**Technically.** Model **default-deny** hai. Policy compiler **pure aur deterministic** hai
(`policyspec.Compiled`) — same input, same bytes, isliye steady-state reconcile no-op hota hai. Compiled
artifact gateway tak jata hai; agent use `ip tunnex` chain mein enforce karta hai.

**Building blocks:**
- **Resources** — jo cheez protect karni hai
- **Groups** — subjects (local ya IdP-synced)
- **Rules** — subject → resource, **port-scoped**
- **Per-rule toggle** — rule delete kiye bina off (`setPolicyRuleEnabled`)
- **Time-boxed grants** — per-user, temporary; sweep pe delete, aur sweep push gateway tak pahunchta hai
- **`extendGrant`** — grant badhao

**Kyun.** Yahi Tunnex ko "VPN" se "Zero Trust" banata hai. VPN network deta hai; ZT batata hai ki
**contractor sirf ek Postgres port** tak pahunche, aur wo bhi Friday tak.

⛔ **Edition-gated** — open binary pe `403 edition_required`, *"Zero Trust policy is a Tunnex Enterprise
feature"*. ⭐ **Par band-gated NAHI** — Community pe poora ZT engine free hai (upar wali table).

### Device posture aur device approval

**Click.** Devices → Pending. Org Settings → posture checks.
API: `listPendingDevices` · `approveDevice` · `rejectDevice` · `getDeviceApproval` / `setDeviceApproval` ·
`listHealthChecks` · `putHealthCheck` · `reportDeviceHealth`.

**Technically.** Approval **on** ho to naya device `pending` mein baithta hai, `/32` reserve karke. Posture
checks warn ya require mode mein — require mode access rok sakta hai.

⛔ **Downgrade releases enforcement — ye law hai.** Licence/edition gir jaye to enforcement chhod deta hai,
lock nahi karta. Ek commercial state kisi ko bahar nahi kar sakti.

⭐ **Unlock-then-opt-in** — dono default OFF hain.

⚠ **Health/posture open build mein कभी surface nahi hoti** — `d.Health = nil` set hota hai taki enterprise
rows leak na hon.

**Ab: policy chal rahi hai. Aage: non-human principals.**

---

# PART 6 — AI AGENTS

**Click.** AI agents → agent enrol karo. (Device with `kind='agent'`.)

**Technically.** AI agent ek **peer** hai gateway pe — laptop se sirf `kind` mein alag. Wo kind do cheezein
carry karta hai: **cap exemption** (agent rows human device cap mein count nahi hote, warna ek admin ka
personal allowance poori fleet pe kharch ho jata) aur **policy source** ban sakna.

**Kyun.** AI agent ko production database chahiye, par usse VPN credential dena aur uspe policy na lagana
sabse buri combination hai. Yahan wo ek first-class subject hai — usi engine se governed.

⚠ **FOUNDER MUST TRY.** Maine `kind='agent'` ka cap-exemption test aur handler padhe hain; live agent
enrol nahi kiya.

---

# PART 7 — KUBERNETES

**Click.** Kubernetes → cluster register karo, phir services expose karo.
API: `registerK8sCluster` · `listK8sServices` · `exposeK8sService` · `unexposeK8sService`.
**CRDs (verified on disk):** `tunnex.io_tunnexclusters.yaml` · `tunnex.io_tunnexexposedservices.yaml` ·
`tunnex.io_tunnexgrants.yaml`.

**Technically.** Operator (`apps/operator`) cluster mein chalta hai, CRDs watch karta hai, aur services ko
Tunnex resources ke roop mein expose karta hai. K8s sweep **<5s push path** pe jata hai, ~25s long-poll pe
nahi.

**Kyun.** Platform team ko dashboard nahi chahiye — unhe YAML chahiye. `TunnexGrant` ek CRD hai, matlab
access GitOps mein rehta hai.

⚠ **FOUNDER MUST TRY.** CRDs disk pe verified hain; maine live cluster pe operator nahi chalaya.

---

# PART 8 — OPENVPN

**Click.** Org Settings → OpenVPN enable. Devices → Export OVPN profile.
API: `setOVPNEnabled` · `exportOVPNProfile`.

**Technically.** OpenVPN **ZTNA-enforced transport** hai — wahi policy engine, alag data plane. Revocation
CRL se hoti hai, aur CRL revoked cert rows se **derive** hoti hai. Multi-remote HA support hai.

⛔ **Isiliye revoked device ka hard delete allowed nahi hai.** `ovpn_client_certs` pe FK `ON DELETE CASCADE`
hai — hard delete us serial ko CRL se nikal deta aur credential wire pe **un-revoke** ho jata. Product ka
apna comment: *"a delete that cascades into a revocation list is an un-revoke wearing a housekeeping
verb."* Isliye device removal **soft** hai.

**Kyun.** Purane routers, appliances, aur wo jagahein jahan WireGuard nahi chal sakta.

⭐ **Unlock-then-opt-in** — org ne opt-in nahi kiya to screen kehti hai *"This organization has not opted
into OpenVPN, so there is no service to report"*, green nahi dikhati.

⚠ **OVPN liveness telemetry registered-but-unbuilt.**

---

# PART 9 — DEKHNA: Access Events, Audit Log, Prometheus

**Click.** Access Events · Audit Log · aur `/metrics` scrape karo.

**Technically.**
- **Access Events** (`listAccessEvents`) — kaun kya reach kar raha hai. Flow/access logs PG-only.
- **Audit Log** (`listAuditLogs`) — kisne kya kiya. ⭐ **System actors first-class hain** (`actor_system`)
  ek **cause** ke saath — matlab "system ne kiya" ke saath "kyun kiya" bhi likha hai.
- **Prometheus** — alag listener, `/metrics` (`metrics/server.go:52`).

**Kyun.** Auditor poochhta hai "kisne access diya aur kab" — aur uska jawab query karne layak hona chahiye.

⛔ **Access log query edition-gated hai** (`access_log_handlers.go`). Community pe Access Events free hain
(tier table), par open **binary** pe query surface nahi hai.

⚠ **Metrics teen tarah split hain** — L1 (S8.5), L2 (S7.5.1b), L3 (S11.1). Sab nahi bane.

---

# PART 10 — LICENSING

**Click.** Settings → Licence → key paste karo. API: `getLicense` · `installLicense`.

**Technically.** **Offline Ed25519 verification** — deployment kabhi humein contact nahi karta, isliye key
air-gapped control plane pe kaam karti hai. Ceiling `CountLiveNodes` pe check hota hai, jo **poore
deployment** ke live gateways ginta hai, ek org ke nahi.

⛔ **Kyun deployment-scoped:** pehle ye per-org tha, aur Starter 5 gateways + unlimited orgs deta hai —
matlab asli ceiling 5 × N tha, aur usse badhane wala button product ke apne header mein tha ("+ New"). Koi
exploit nahi, koi hacking nahi. Ab ceiling deployment-wide hai.

**Ceilings ki table upar hai.** Grace: licence expire hone pe **chalti hui cheez kabhi band nahi hoti** —
sirf naya enrol rukta hai.

⭐ **Refusal ke saath rasta milta hai:** `gateway_limit_reached` / `org_limit_reached` pe UI "Install a
licence key" aur "Request a licence" dikhata hai. *A limit without a route is a dead end. A limit with one
is a price.*

### ⛔ Ek live defect, is step pe

**Gateways page ka ceiling notice org-scoped count ko deployment-scoped ceiling ke saath pair karta hai.**
127 live gateways aur ceiling 2 wale box pe wo **at-ceiling** wala sentence dikhata hai — matlab
over-ceiling branch, jo kehne ke liye bana tha *"revoking one will not free a slot"*, **kabhi fire nahi
karta**. Operator ek chalta hua gateway revoke karega, retry karega, fail hoga.

**Unfixed. Disposition ke liye held.**

---

# PART 11 — GATEWAY LIFECYCLE: transfer, revoke, delete

## 11.1 Transfer (revoke se pehle)

**Click.** Gateways → Revoke → agar devices homed hain to **"Move to"** picker + **Move devices**.
API: `transferNodeDevices`, perm `device:transfer`.

**Technically.** `active` aur `pending` dono devices move hote hain. **Status nahi badalta** — pending
pending rehta hai, kyunki approval **banda** ke baare mein hai, gateway ke baare mein nahi. **Address
reallocate nahi hota** — pool org-scoped hai, to same-org move collide kar hi nahi sakta. Dono gateways
reconcile hote hain. **Ek audit event** — count aur dono gateways ke naam ke saath.

⛔ **Aur response per-device batata hai ki config re-issue chahiye ya nahi.** Row move karna aasan half hai;
jis device ka config purane gateway ka naam leta ho wo **database mein moved aur wire pe toota** hai.

## 11.2 Revoke

**Click.** Gateways → Revoke → Confirm.

**Technically.** Revoke ab **refuse** karta hai jab tak koi device homed hai —
`409 devices_still_homed`, count ke saath. Warna cascade chalta hai: `RevokeDevicesForNode` **usi
transaction mein** har active+pending device ko `revoked_cause='cascade'` se revoke karta hai. Gateway
revoke pe hub set re-elect hota hai aur CRL rebuild hoti hai.

⛔ **Revoked gateway kabhi active nahi hota. Un-revoke permanently refused hai.** Recovery ka matlab hai naya
join-token enrolment, jo naya node banata hai.

⭐ **Transfer-first isliye ruled hua kyunki ordering ka faisla "aadha chhod ke chale gaye to kya bachta hai"
se hota hai:** transfer-first pe "devices moved, purana gateway abhi bhi chal raha hai" — harmless aur
resumable. Revoke-then-restore pe "poori fleet disconnected aur gateway wapas nahi aa sakta" — ek outage
jise product undo nahi kar sakta.

## 11.3 Delete aur rename

**Click.** Revoked row pe **Delete**. Naam pe **Rename**.
API: `deleteNode` · `updateNode`.

**Technically.** Delete **sirf revoked gateway** pe. Wahi predicate poora safety argument hai: revoke khud
refuse karta hai jab devices homed hon, to revoked hone tak transfer ho chuka hota hai aur cascading FKs ke
paas destroy karne ko kuch bacha hi nahi. **Enrolment token bhi delete hota hai** — wo `ON DELETE SET NULL`
hai, matlab unlinked survive karke phir bhi gateway enrol kar sakta tha.

⛔ **Rename hai, endpoint edit NAHI hai.** Config jis endpoint pe issue hua tha uska koi snapshot nahi hai,
to endpoint edit `needs_reexport` ko dikhta hi nahi — har affected user ko pata connect fail hone pe chalta.
**Registered as S12.12b.** Abhi endpoint theek karne ka rasta re-enrolment hai, aur wo ek licence slot leta
hai jab tak purana revoke na ho.

---

# PART 12 — BACKUP AUR RESTORE

**Docs:** `docs/backup-restore.md` (verified on disk).

**Technically.** CP backup **do artifacts** hain aur **dono rakhne padte hain**:
1. Database dump
2. **Master key** — 32-byte secret, backup ke **bahar**, jo ekmatra cheez hai jo dump ko decrypt kar sakti
   hai

⛔ **Master key deliberately backup ke andar NAHI hai.** Jo backup apni key khud leke chalta hai wo
encryption-at-rest ke barabar hai hi nahi — aur backups sabse zyada copy hone wali, sabse kam guarded file
hoti hai.

⭐ **Backup mein fleet ke secrets nahi hain** — wo control plane ki state restore karta hai, fleet ki nahi,
aur usse zarurat bhi nahi: agents apne keys khud rakhte hain aur reconcile kar lete hain.

⚠ **FOUNDER MUST TRY.** Maine doc padha hai. **Ek restore drill kabhi nahi chala** — aur ek untested
backup, backup nahi hota.

---

# ⛔ AAKHIR MEIN — jo verify nahi hua

**Code se verified:** sections 0–1.5 poore, plus har jagah ke handlers, queries, gates, ceilings, CRD files,
aur email wire format.

**⚠ FOUNDER MUST TRY (live nahi chalaya):** forced password change wall · real SSO round-trip (Google, Entra)
· directory sync live · QR/mobile connect · desktop client connect · AI agent enrol · K8s operator live
cluster pe · restore drill · aur **sabse zaroori: invitation accept → sign-in → lands-in-org, real mail ke
saath**.

**Registered-but-unbuilt, ek jagah:**
- S12.12b — gateway endpoint edit (issued endpoint ka snapshot nahi hai)
- Google directory sync + SCIM (S7.5.2b)
- OVPN liveness telemetry
- Failover hysteresis persistence (beta-blocking)
- Windows full-tunnel re-home carve-out (S8.6b)
- Code signing / notarization (trigger: public beta)
- MFA SSO-exempt wire proof; WF-2 break-glass
- Metrics L1/L2/L3 ka baaki hissa

**Live defects, disposition ke liye held:**
- Gateways page ka ceiling numerator org-scoped hai, ceiling deployment-scoped
- `delivered` ka koi web consumer nahi
- `ResendInvitation` token nahi deta
- `go/email-injection` at `buildRFC822`
- Fresh install pe signup khula rehta hai bootstrap admin ke hote hue, aur `/auth/signup` pe rate limit nahi
