//go:build windows

package wfp

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ── S6.7 delta: FIXED provider + sublayer GUIDs ──────────────────────────────────────────
//
// These REPLACE upstream registerBaseObjects' random windows.GenerateGUID() (see blocker.go).
// A persistent block armed under a RANDOM key is unfindable after the process dies — the whole
// reason the Windows kill-switch leaked on crash. These fixed keys are the durable anchor that
// CleanStale / --wfp-clean enumerate + delete by. NEVER change them: a changed GUID orphans
// blocks a prior build armed (removable only by the old build or a reboot with old CleanStale).
// Generated once, 2026-07-11.
var (
	tunnexProviderGUID = windows.GUID{Data1: 0x2fed20c6, Data2: 0x725d, Data3: 0x4b5a, Data4: [8]byte{0x93, 0xaa, 0xf8, 0xe6, 0xed, 0x8a, 0xc2, 0xf4}}
	tunnexSublayerGUID = windows.GUID{Data1: 0x116ff67b, Data2: 0x77cb, Data3: 0x466e, Data4: [8]byte{0x91, 0x44, 0x97, 0xfc, 0xa3, 0x9b, 0x52, 0xf4}}
)

// providerRecoveryHint is the provider's WFP display DESCRIPTION. It shows in
// `netsh wfp show state`, so a locked-out operator inspecting WFP sees the exact recovery
// command ON the box (S6.7 discoverability requirement).
const providerRecoveryHint = "Tunnex full-tunnel kill-switch — to remove, run as admin: tunnex-helper.exe --wfp-clean"

// ── S6.7 delta: enumerate-and-delete cleanup + the FWPM delete/enum syscalls upstream lacked ─

// wtFwpmFilterEnumTemplate0 mirrors FWPM_FILTER_ENUM_TEMPLATE0 (amd64/arm64 layout — the Windows
// client is x64). Only providerKey is set: a null layerKey enumerates that provider's filters
// across all layers. actionMask 0xFFFFFFFF matches any action.
type wtFwpmFilterEnumTemplate0 struct {
	providerKey             *windows.GUID
	layerKey                windows.GUID
	enumType                int32 // FWP_FILTER_ENUM_TYPE; FWP_FILTER_ENUM_OVERLAPPING == 0
	flags                   uint32
	providerContextTemplate uintptr
	numFilterConditions     uint32
	_                       uint32 // pad so the next pointer is 8-aligned (64-bit)
	filterCondition         uintptr
	actionMask              uint32
	_                       uint32 // pad
	calloutKey              *windows.GUID
}

const cFWP_FILTER_ENUM_OVERLAPPING = 0

var (
	procFwpmFilterCreateEnumHandle0  = modfwpuclnt.NewProc("FwpmFilterCreateEnumHandle0")
	procFwpmFilterEnum0              = modfwpuclnt.NewProc("FwpmFilterEnum0")
	procFwpmFilterDestroyEnumHandle0 = modfwpuclnt.NewProc("FwpmFilterDestroyEnumHandle0")
	procFwpmFilterDeleteById0        = modfwpuclnt.NewProc("FwpmFilterDeleteById0")
	procFwpmSubLayerDeleteByKey0     = modfwpuclnt.NewProc("FwpmSubLayerDeleteByKey0")
	procFwpmProviderDeleteByKey0     = modfwpuclnt.NewProc("FwpmProviderDeleteByKey0")
)

func fwpmFilterCreateEnumHandle0(engine uintptr, template *wtFwpmFilterEnumTemplate0, enumHandle *uintptr) error {
	r1, _, e1 := syscall.SyscallN(procFwpmFilterCreateEnumHandle0.Addr(), engine, uintptr(unsafe.Pointer(template)), uintptr(unsafe.Pointer(enumHandle)))
	if r1 != 0 {
		return errnoErr(e1)
	}
	return nil
}

func fwpmFilterDestroyEnumHandle0(engine, enumHandle uintptr) error {
	r1, _, e1 := syscall.SyscallN(procFwpmFilterDestroyEnumHandle0.Addr(), engine, enumHandle)
	if r1 != 0 {
		return errnoErr(e1)
	}
	return nil
}

func fwpmFilterEnum0(engine, enumHandle uintptr, numRequested uint32, entries ***wtFwpmFilter0, numReturned *uint32) error {
	r1, _, e1 := syscall.SyscallN(procFwpmFilterEnum0.Addr(), engine, enumHandle, uintptr(numRequested), uintptr(unsafe.Pointer(entries)), uintptr(unsafe.Pointer(numReturned)))
	if r1 != 0 {
		return errnoErr(e1)
	}
	return nil
}

func fwpmFilterDeleteById0(engine uintptr, id uint64) error {
	r1, _, e1 := syscall.SyscallN(procFwpmFilterDeleteById0.Addr(), engine, uintptr(id))
	if r1 != 0 {
		return errnoErr(e1)
	}
	return nil
}

func fwpmSubLayerDeleteByKey0(engine uintptr, key *windows.GUID) error {
	r1, _, e1 := syscall.SyscallN(procFwpmSubLayerDeleteByKey0.Addr(), engine, uintptr(unsafe.Pointer(key)))
	if r1 != 0 {
		return errnoErr(e1)
	}
	return nil
}

func fwpmProviderDeleteByKey0(engine uintptr, key *windows.GUID) error {
	r1, _, e1 := syscall.SyscallN(procFwpmProviderDeleteByKey0.Addr(), engine, uintptr(unsafe.Pointer(key)))
	if r1 != 0 {
		return errnoErr(e1)
	}
	return nil
}

// Clean removes the persistent Tunnex WFP kill-switch objects, surfacing any hard error (engine
// open / transaction). This is the exported entry for the startup CleanStale and the
// `tunnex-helper --wfp-clean` escape hatch — the SAME code path, so the hatch is exactly what
// startup self-heal does. Idempotent (nothing armed → nil).
func Clean() error { return removePersistentObjects() }

// ArmBlockAll arms a MINIMAL persistent block-all under the Tunnex fixed provider/sublayer —
// DEV ONLY, for the S6.7 deliberate-red: `tunnex-helper --wfp-arm-test` wedges the box (kills
// egress) so the escape hatch can be PROVEN to un-wedge a genuinely dead box before the persistent
// arming is trusted. It deliberately SKIPS permitWireGuardService (which needs the SERVICE's own
// SID in the token — absent in a standalone admin CLI, giving "specified group does not exist"),
// so it can run outside the service. It uses the SAME non-dynamic session + fixed GUID as the live
// arm, so it exercises the identical persistence + cleanup path. Recover with Clean() /
// `--wfp-clean` / reboot.
func ArmBlockAll() error {
	_ = removePersistentObjects() // clean any prior block first (self-heal)
	session, err := createWfpSession()
	if err != nil {
		return wrapErr(err)
	}
	err = runTransaction(session, func(session uintptr) error {
		bo, err := registerBaseObjects(session) // provider + sublayer, fixed GUID
		if err != nil {
			return wrapErr(err)
		}
		return blockAll(session, bo, 0) // block everything — no permits
	})
	if err != nil {
		fwpmEngineClose0(session)
		return wrapErr(err)
	}
	fwpmEngineClose0(session) // close — non-dynamic objects persist
	return nil
}

// removePersistentObjects is the crash-safe cleanup: it deletes EVERY WFP object under the
// Tunnex fixed provider/sublayer — our filters (enumerated by provider), then the sublayer and
// provider by key. It opens its OWN engine, so it works with no live session (after the arming
// process died) — this is BOTH DisableFirewall (graceful) and CleanStale (startup + the
// --wfp-clean escape hatch). Idempotent: nothing armed → the deletes no-op. Returns an error
// only if it can't even open the engine / run the transaction (so callers can log it).
func removePersistentObjects() error {
	var engine uintptr
	sess := wtFwpmSession0{txnWaitTimeoutInMSec: windows.INFINITE} // non-dynamic
	if err := fwpmEngineOpen0(nil, cRPC_C_AUTHN_WINNT, nil, &sess, unsafe.Pointer(&engine)); err != nil {
		return wrapErr(err)
	}
	defer fwpmEngineClose0(engine)

	// Phase 1 (read): collect our filter IDs via an enum handle (no txn — enumeration is a read).
	ids := collectTunnexFilterIDs(engine)

	// Phase 2 (write): delete filters, then sublayer, then provider, in one transaction. Order
	// matters — WFP refuses to delete a sublayer/provider still referenced by filters.
	if err := fwpmTransactionBegin0(engine, 0); err != nil {
		return wrapErr(err)
	}
	for _, id := range ids {
		_ = fwpmFilterDeleteById0(engine, id) // ignore not-found
	}
	_ = fwpmSubLayerDeleteByKey0(engine, &tunnexSublayerGUID)
	_ = fwpmProviderDeleteByKey0(engine, &tunnexProviderGUID)
	if err := fwpmTransactionCommit0(engine); err != nil {
		_ = fwpmTransactionAbort0(engine)
		return wrapErr(err)
	}
	return nil
}

// collectTunnexFilterIDs returns the runtime IDs of every filter under the Tunnex provider. It
// enumerates ALL filters with a NIL template and matches our provider IN GO — deliberately
// avoiding the FWPM_FILTER_ENUM_TEMPLATE0 layout (an OVERLAPPING template with a null layer
// returned nothing in testing, so cleanup deleted nothing). Best-effort: an enum failure returns
// what was collected so far.
func collectTunnexFilterIDs(engine uintptr) []uint64 {
	var enumHandle uintptr
	if err := fwpmFilterCreateEnumHandle0(engine, nil, &enumHandle); err != nil {
		return nil
	}
	defer fwpmFilterDestroyEnumHandle0(engine, enumHandle)

	var ids []uint64
	for {
		var entries **wtFwpmFilter0
		var num uint32
		if err := fwpmFilterEnum0(engine, enumHandle, 128, &entries, &num); err != nil || num == 0 {
			return ids
		}
		for _, f := range unsafe.Slice(entries, num) {
			if f.providerKey != nil && *f.providerKey == tunnexProviderGUID {
				ids = append(ids, f.filterID)
			}
		}
		fwpmFreeMemory0(unsafe.Pointer(&entries))
		if num < 128 {
			return ids
		}
	}
}
