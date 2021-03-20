package server

import (
	"crypto/subtle"
	"net/http"
)

func BasicAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(user), []byte(AdminUser)) != 1 ||
			subtle.ConstantTimeCompare([]byte(pass), []byte(AdminPassword)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="`+Realm+`"`)
			w.WriteHeader(401)
			w.Write([]byte("You are unauthorized to access the application.\n"))
			return
		}
		handler(w, r)
	}
}
