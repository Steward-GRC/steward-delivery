// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"errors"
	"net/http"
	"strings"

	log "github.com/Bugs5382/go-log"
	"google.golang.org/grpc/codes"

	"github.com/Steward-GRC/steward-delivery/internal/workloadauth"
)

// HTTPAuth guards an internal HTTP port with the same workload tokens as the
// gRPC API: the request must carry "Authorization: Bearer <token>", the token
// must verify, and the caller must be in callers. A missing or rejected token
// is 401, a caller not on the list 403, and a verifier with no key set yet
// 503. Each refusal is logged and passed to onDeny (nil skips it). The
// handler runs with the Grant in its context.
func HTTPAuth(v workloadauth.TokenVerifier, callers []string, lg log.Logger, onDeny workloadauth.DenyHook) func(http.Handler) http.Handler {
	if lg == nil {
		lg = log.Nop()
	}
	lg = lg.With(log.F("component", "httpauth"))
	allowed := make(map[string]bool, len(callers))
	for _, c := range callers {
		allowed[c] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			route := r.Method + " " + r.URL.Path
			deny := func(status int, d workloadauth.Denial) {
				d.Method = route
				lg.Ctx(r.Context()).Warn("request refused",
					log.F("route", route), log.F("caller", d.Caller.Name), log.F("service_account", d.Caller.ServiceAccount),
					log.F("status", status), log.F("reason", d.Reason))
				if onDeny != nil {
					onDeny(r.Context(), d)
				}
				if status == http.StatusUnauthorized {
					w.Header().Set("WWW-Authenticate", "Bearer")
				}
				http.Error(w, d.Reason, status)
			}
			tok, ok := bearerHeader(r)
			if !ok {
				deny(http.StatusUnauthorized, workloadauth.Denial{Code: codes.Unauthenticated, Reason: workloadauth.ReasonNoToken})
				return
			}
			c, err := v.Verify(tok)
			switch {
			case errors.Is(err, workloadauth.ErrUnavailable):
				deny(http.StatusServiceUnavailable, workloadauth.Denial{Code: codes.Unavailable, Reason: workloadauth.ReasonUnavailable})
				return
			case err != nil:
				deny(http.StatusUnauthorized, workloadauth.Denial{Code: codes.Unauthenticated, Reason: workloadauth.ReasonBadToken})
				return
			case !allowed[c.Name]:
				deny(http.StatusForbidden, workloadauth.Denial{Caller: c, Code: codes.PermissionDenied, Reason: workloadauth.ReasonMethodNotAllowed})
				return
			}
			log.Trace(lg.Ctx(r.Context()), "request authorized", log.F("route", route), log.F("caller", c.Name))
			next.ServeHTTP(w, r.WithContext(workloadauth.ContextWithGrant(r.Context(), workloadauth.Grant{Caller: c, Access: workloadauth.Self})))
		})
	}
}

func bearerHeader(r *http.Request) (string, bool) {
	vals := r.Header.Values("Authorization")
	if len(vals) != 1 {
		return "", false
	}
	scheme, tok, found := strings.Cut(strings.TrimSpace(vals[0]), " ")
	tok = strings.TrimSpace(tok)
	if !found || !strings.EqualFold(scheme, "Bearer") || tok == "" {
		return "", false
	}
	return tok, true
}
