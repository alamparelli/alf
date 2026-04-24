package handle

// ResetMintForTesting clears the process-wide one-shot mint state so a
// test can re-exercise the MintRuntimeToken / ForgeInstance path from
// scratch. PRODUCTION CODE MUST NOT CALL THIS — the one-shot invariant
// is a deliberate safety net against accidental double-mint.
//
// This lives in a non-_test.go file (rather than inside forge_test.go's
// resetMintStateForTest) because tests in OTHER packages — specifically
// internal/runtime/ — need to reset the minter between table-driven
// Instantiator tests. Those tests cannot reach an unexported helper.
//
// The export cost is tolerable:
//   - Archtest TestMintRuntimeTokenIsRuntimeOnly forbids calling
//     MintRuntimeToken from anywhere outside Runtime + handle subtree,
//     so the mint state cannot be exploited externally.
//   - The name announces intent loudly enough that a grep or review
//     will flag any production reference.
//
// A future pass may swap this for a build-tag-gated _testhook.go or a
// first-class "scope" variant of the minter. Keeping it simple for now.
func ResetMintForTesting() {
	mintLock.Store(false)
	mintedOK.Store(false)
	mintedToken = RuntimeToken{}
}
