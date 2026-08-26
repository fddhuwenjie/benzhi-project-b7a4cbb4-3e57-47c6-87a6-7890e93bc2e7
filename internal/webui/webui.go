package webui

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed assets/*
var assets embed.FS

type asset struct {
	data         []byte
	contentType  string
	etag         string
	cacheControl string
}

type Handler struct {
	assets map[string]asset
}

func NewHandler() *Handler {
	handler := &Handler{assets: map[string]asset{}}
	err := fs.WalkDir(assets, "assets", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := assets.ReadFile(name)
		if err != nil {
			return err
		}
		publicName := strings.TrimPrefix(name, "assets/")
		sum := sha256.Sum256(data)
		contentType := mime.TypeByExtension(path.Ext(publicName))
		if contentType == "" {
			contentType = http.DetectContentType(data)
		}
		cacheControl := "public, max-age=3600, must-revalidate"
		if publicName == "index.html" {
			cacheControl = "no-cache"
		}
		handler.assets[publicName] = asset{
			data: data, contentType: contentType,
			etag: `"` + hex.EncodeToString(sum[:]) + `"`, cacheControl: cacheControl,
		}
		return nil
	})
	if err != nil {
		panic(err)
	}
	if _, exists := handler.assets["index.html"]; !exists {
		panic("webui 缺少 index.html")
	}
	return handler
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	requested := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if requested == "." || requested == "" {
		requested = "index.html"
	}
	entry, exists := h.assets[requested]
	if !exists && path.Ext(requested) == "" {
		requested = "index.html"
		entry, exists = h.assets[requested]
	}
	if !exists {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", entry.contentType)
	w.Header().Set("Cache-Control", entry.cacheControl)
	w.Header().Set("ETag", entry.etag)
	if r.Header.Get("If-None-Match") == entry.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	http.ServeContent(w, r, requested, time.Time{}, bytes.NewReader(entry.data))
}
