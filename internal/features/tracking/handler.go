package tracking

import (
	"encoding/json"
	"net/http"
	"strings"
)

type Handler struct {
	Service *Service
}

func NewHandler(s *Service) *Handler {
	return &Handler{Service: s}
}

func (h *Handler) GetOrderStatus(w http.ResponseWriter, r *http.Request) {
	orderNum := strings.TrimPrefix(r.URL.Path, "/orders/")
	orderNum = strings.TrimSuffix(orderNum, "/status")

	resp, err := h.Service.GetOrderStatus(r.Context(), orderNum)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if resp == nil {
		http.Error(w, "Order not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) GetOrderHistory(w http.ResponseWriter, r *http.Request) {
	orderNum := strings.TrimPrefix(r.URL.Path, "/orders/")
	orderNum = strings.TrimSuffix(orderNum, "/history")

	resp, err := h.Service.GetOrderHistory(r.Context(), orderNum)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) GetWorkersStatus(w http.ResponseWriter, r *http.Request) {
	resp, err := h.Service.GetWorkersStatus(r.Context())
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}