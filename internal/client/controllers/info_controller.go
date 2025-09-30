package controllers

import (
	"encoding/json"
	"net/http"

	"github.com/formancehq/go-libs/v2/logging"
)

type StargateControllerConfig struct {
	version string
}

func NewStargateControllerConfig(
	version string,
) StargateControllerConfig {
	return StargateControllerConfig{
		version: version,
	}
}

type StargateController struct {
	config StargateControllerConfig
}

func NewStargateController(
	config StargateControllerConfig,
) *StargateController {
	return &StargateController{
		config: config,
	}
}

type ServiceInfo struct {
	Version string `json:"version"`
}

func (s *StargateController) GetInfo(w http.ResponseWriter, r *http.Request) {
	info := ServiceInfo{
		Version: s.config.version,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(info); err != nil {
		logging.FromContext(r.Context()).Errorf("failed to encode info response: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}
