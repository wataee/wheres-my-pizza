package order

import (
	"encoding/json"
	"net/http"
	"restaurant-system/internal/constants"
	"restaurant-system/internal/logger"
	"restaurant-system/internal/models"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Handler handles HTTP requests for order service
type Handler struct {
	Service *Service
	DB      *pgxpool.Pool
}

// NewHandler creates a new order handler
func NewHandler(s *Service, db *pgxpool.Pool) *Handler {
	return &Handler{
		Service: s,
		DB:      db,
	}
}

// CreateOrder handles POST /orders
func (h *Handler) CreateOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Generate unique request ID for tracing
	requestID := GenerateRequestID()

	// Parse request body
	var req models.OrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.LogError("validation_failed", "Invalid JSON body", err, requestID)
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Invalid JSON format",
		})
		return
	}

	// Create order
	resp, err := h.Service.CreateOrder(r.Context(), req, requestID)
	if err != nil {
		if err.Error() == "internal system error" {
			respondJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "Internal server error",
			})
		} else {
			respondJSON(w, http.StatusBadRequest, map[string]string{
				"error": err.Error(),
			})
		}
		return
	}

	// Success response
	respondJSON(w, http.StatusOK, resp)
}

// HealthCheck handles GET /health
func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	
	checks := make(map[string]string)
	allHealthy := true

	// Check database
	if err := h.DB.Ping(ctx); err != nil {
		checks["database"] = "unhealthy: " + err.Error()
		allHealthy = false
	} else {
		checks["database"] = "healthy"
	}

	// Determine overall status
	status := "healthy"
	httpStatus := http.StatusOK
	if !allHealthy {
		status = "unhealthy"
		httpStatus = http.StatusServiceUnavailable
	}

	response := models.HealthCheckResponse{
		Status:    status,
		Timestamp: time.Now().UTC(),
		Service:   constants.ServiceOrderService,
		Checks:    checks,
	}

	respondJSON(w, httpStatus, response)
}

// respondJSON writes a JSON response
func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	
	if err := json.NewEncoder(w).Encode(data); err != nil {
		logger.LogError("response_encoding_failed", "Failed to encode response", err, "")
	}
}
