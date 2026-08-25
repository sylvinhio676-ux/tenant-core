package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHealthzHandler_AlwaysReturnsOK(t *testing.T) {
	// /healthz must report ok regardless of readiness — it's a liveness
	// probe, not a readiness probe. Checked in both states.
	for _, readyState := range []bool{false, true} {
		ready.Store(readyState)

		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		recorder := httptest.NewRecorder()

		healthzHandler(recorder, req)

		assert.Equal(t, http.StatusOK, recorder.Code, "ready=%v", readyState)
		assert.JSONEq(t, `{"status":"ok"}`, recorder.Body.String())
	}
}

func TestReadyzHandler_ReflectsReadyState(t *testing.T) {
	ready.Store(false)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	recorder := httptest.NewRecorder()
	readyzHandler(recorder, req)

	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	assert.JSONEq(t, `{"status":"not ready"}`, recorder.Body.String())

	ready.Store(true)

	req = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	recorder = httptest.NewRecorder()
	readyzHandler(recorder, req)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, `{"status":"ready"}`, recorder.Body.String())
}
