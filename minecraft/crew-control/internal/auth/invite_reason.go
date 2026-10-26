package auth

const (
	codeInviteRefused = "invite_refused"
	codeInviteUsed    = "invite_used"
)

// refusal names why an invite was turned down. Only the operator's true,
// already-spent code is called used, so a guesser learns nothing a wrong
// code would not have told them.
func (iv *Invites) refusal(op, code string) string {
	iv.mu.Lock()
	defer iv.mu.Unlock()
	if iv.matches(op, code) && iv.used[codeHash(code)] {
		return codeInviteUsed
	}
	return codeInviteRefused
}
