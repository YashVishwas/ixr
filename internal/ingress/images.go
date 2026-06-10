package ingress

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/YashVishwas/ixr/pkg/provider"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// ImagesHandler handles POST /v1/images/generations.
type ImagesHandler struct {
	router Router
}

// NewImagesHandler creates an image generation handler.
func NewImagesHandler(r Router) *ImagesHandler {
	return &ImagesHandler{router: r}
}

func (h *ImagesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is supported")
		return
	}

	var req schema.ImageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_body", "could not parse request JSON")
		return
	}
	if req.Model == "" {
		writeError(w, http.StatusBadRequest, "missing_model", "model field is required")
		return
	}

	p, err := h.router(req.Model)
	if err != nil {
		writeError(w, http.StatusBadRequest, "no_provider", err.Error())
		return
	}

	gen, ok := p.(provider.ImageGenerator)
	if !ok {
		writeError(w, http.StatusNotImplemented, "not_supported", "provider "+p.Name()+" does not support image generation")
		return
	}

	resp, err := gen.GenerateImage(r.Context(), &req)
	if err != nil {
		slog.Error("image generation error", "provider", p.Name(), "model", req.Model, "err", err)
		writeError(w, http.StatusBadGateway, "provider_error", "upstream provider returned an error")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
