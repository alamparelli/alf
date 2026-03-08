package cli

import "fmt"

// RunToken prints the CC bearer token for API/mobile app access.
func RunToken() {
	dir := alfDir()
	token := GetSecret(dir, "cc_auth_token")
	if token == "" {
		Fatal("cc_auth_token not set. Run: alf token reset")
	}
	fmt.Println(token)
}

// RunTokenReset generates a new bearer token and saves it.
// Requires restart to take effect.
func RunTokenReset() {
	dir := alfDir()
	token, err := generateAuthToken()
	if err != nil {
		Fatal(fmt.Sprintf("Failed to generate token: %v", err))
	}
	if err := SetSecret(dir, "cc_auth_token", token); err != nil {
		Fatal(fmt.Sprintf("Failed to save token: %v", err))
	}
	fmt.Println(token)
	PrintWarning("Restart ALF for the new token to take effect: alf restart")
}
