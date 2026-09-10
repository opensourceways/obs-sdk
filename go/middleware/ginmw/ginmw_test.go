package ginmw

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensourceways/obs-sdk/go/metrics"
	"github.com/opensourceways/obs-sdk/go/sdkctx"
)

func noProcess() *bool { b := false; return &b }

func TestGinMiddlewareRequestIDAndCommunity(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var (
		gotRid string
		gotC   string
	)
	r := gin.New()
	r.Use(Middleware(Options{
		Metrics: metrics.New(metrics.Config{
			Service: "review", Community: "openeuler", IncludeProcess: noProcess(),
		}),
		ResolveCommunity: func(c *gin.Context) string { return "openEuler" },
	}))
	r.GET("/ping", func(c *gin.Context) {
		gotRid = sdkctx.RequestID(c.Request.Context())
		gotC = sdkctx.Community(c.Request.Context())
		c.String(http.StatusOK, "pong")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.NotEmpty(t, gotRid)
	assert.Equal(t, "openEuler", gotC)
}
