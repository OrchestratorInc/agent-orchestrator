package runner

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func runnerPolicy(managementKey string, capability *routeCapability, selectorReady func() bool, credentialAlive func(string, string) bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		path, method := c.Request.URL.Path, c.Request.Method
		if strings.HasPrefix(path, "/v0/management/") {
			if !validControlAuthorization(c.GetHeader("Authorization"), managementKey) {
				c.AbortWithStatus(http.StatusUnauthorized)
				return
			}
			if !allowedManagementOperation(method, path) {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			c.Next()
			return
		}
		if path == "/healthz" || strings.HasPrefix(path, "/ao/internal/") {
			c.Next()
			return
		}
		allowed := method == http.MethodGet && path == "/v1/models"
		if method == http.MethodPost {
			switch path {
			case "/v1/responses", "/v1/responses/compact", "/v1/messages", "/v1/messages/count_tokens":
				allowed = true
			}
		}
		if !allowed {
			status := http.StatusNotFound
			if strings.HasPrefix(path, "/v1/") {
				status = http.StatusNotImplemented
			}
			c.AbortWithStatus(status)
			return
		}
		if _, err := capability.Authenticate(c.Request.Context(), c.Request); err != nil {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		if !selectorReady() {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
		claims, err := capability.open(routeTokenFromRequest(c.Request))
		if err != nil || credentialAlive == nil || !credentialAlive(claims.Provider, claims.AuthIndex) {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	}
}

func allowedManagementOperation(method, path string) bool {
	switch method + " " + strings.TrimPrefix(path, "/v0/management/") {
	case "GET routing/strategy":
		return true
	default:
		return false
	}
}
