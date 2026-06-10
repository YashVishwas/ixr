package ingress

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/YashVishwas/ixr/pkg/provider"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// EmbeddingsHandler handles POST /v1/embeddings.
// It is OpenAI-compatible and routes to any provider that implements provider.Embedder.
type EmbeddingsHandler struct {
	router Router
}

// NewEmbeddingsHandler creates an embeddings handler.
func NewEmbeddingsHandler(r Router) *EmbeddingsHandler {
	return &EmbeddingsHandler{router: r}
}

func (h *EmbeddingsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is supported")
		return
	}

	var req schema.EmbeddingRequest
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

	embedder, ok := p.(provider.Embedder)
	if !ok {
		writeError(w, http.StatusNotImplemented, "not_supported", "provider "+p.Name()+" does not support embeddings")
		return
	}

	resp, err := embedder.Embed(r.Context(), &req)
	if err != nil {
		slog.Error("embedding error", "provider", p.Name(), "model", req.Model, "err", err)
		writeError(w, http.StatusBadGateway, "provider_error", "upstream provider returned an error")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
