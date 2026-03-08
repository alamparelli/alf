package cli

import "fmt"

// RunToken prints the CC bearer token for API/mobile app access.
func RunToken() {
	dir := alfDir()
	token := GetSecret(dir, "cc_auth_token")
	if token == "" {
		Fatal("cc_auth_token not set. Run: alf secret set cc_auth_token <value>")
	}
	fmt.Println(token)
}
