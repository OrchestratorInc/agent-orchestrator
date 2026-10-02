package runner

import (
	"errors"
	"net/http"
)

var errCredentialQuotaResponse = errors.New("quota response is invalid")

func writeCredentialQuotaError(w http.ResponseWriter, err error) {
	if errors.Is(err, errCredentialFenced) || errors.Is(err, errCredentialConflict) {
		writeCredentialError(w, err)
		return
	}
	status, code := http.StatusServiceUnavailable, "quota_unavailable"
	var upstream *credentialCheckStatusError
	if errors.As(err, &upstream) {
		switch upstream.status {
		case http.StatusUnauthorized:
			status, code = http.StatusUnauthorized, "quota_authentication_required"
		case http.StatusForbidden:
			status, code = http.StatusForbidden, "quota_access_denied"
		case http.StatusTooManyRequests:
			status, code = http.StatusTooManyRequests, "quota_rate_limited"
		}
	}
	if errors.Is(err, errCredentialQuotaResponse) {
		status, code = http.StatusBadGateway, "quota_response_invalid"
	}
	writeRouteError(w, status, code)
}
