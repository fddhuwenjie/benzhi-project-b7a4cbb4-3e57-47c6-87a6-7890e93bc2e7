package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

type requestIDContextKey struct{}

func Middleware(next http.Handler, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		requestID := acceptedRequestID(r.Header.Get("X-Request-ID"))
		if requestID == "" {
			requestID = newRequestID()
		}
		w.Header().Set("X-Request-ID", requestID)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		ctx := context.WithValue(r.Context(), requestIDContextKey{}, requestID)
		recorder := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("HTTP handler panic", "request_id", requestID, "panic", recovered, "stack", string(debug.Stack()))
				if !recorder.wroteHeader {
					writeJSON(recorder, http.StatusInternalServerError, map[string]any{"error": map[string]string{"code": "internal", "message": "服务内部错误"}})
				}
			}
			level := slog.LevelInfo
			if recorder.status >= 500 {
				level = slog.LevelError
			} else if recorder.status >= 400 {
				level = slog.LevelWarn
			}
			logger.Log(context.Background(), level, "HTTP 请求", "request_id", requestID, "method", r.Method, "path", r.URL.Path, "status", recorder.status, "bytes", recorder.bytes, "duration_ms", time.Since(started).Milliseconds())
		}()
		next.ServeHTTP(recorder, r.WithContext(ctx))
	})
}

func RequestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDContextKey{}).(string)
	return value
}

type responseRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.status, r.wroteHeader = status, true
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(data []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	written, err := r.ResponseWriter.Write(data)
	r.bytes += written
	return written, err
}

func (r *responseRecorder) Flush() {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func acceptedRequestID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return ""
	}
	for _, char := range value {
		if !(char == '-' || char == '_' || char == '.' || char >= '0' && char <= '9' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z') {
			return ""
		}
	}
	return value
}

func newRequestID() string {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "http-" + time.Now().UTC().Format("20060102150405.000000000")
	}
	return "http-" + hex.EncodeToString(buffer)
}
