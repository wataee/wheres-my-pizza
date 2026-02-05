package order

import (
	"encoding/json"
	"net/http"
	"restaurant-system/internal/logger"
	"restaurant-system/internal/models"
)

type Handler struct {
	Service *Service
}

func NewHandler(s *Service) *Handler {
	return &Handler{Service: s}
}

func (h *Handler) CreateOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req models.OrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.LogError("validation_failed", "Invalid JSON body", err, "")
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	resp, err := h.Service.CreateOrder(r.Context(), req)
	if err != nil {
		if err.Error() == "internal system error" {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		} else {
			logger.LogError("validation_failed", "Business validation failed", err, "")
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}