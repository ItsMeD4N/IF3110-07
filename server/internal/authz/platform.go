package authz

import "net/http"

var platformOnly = NewMiddleware(New(nil))

func RequirePlatform(perm Permission, next http.Handler) http.Handler {
	return platformOnly.Require(perm, next)
}
