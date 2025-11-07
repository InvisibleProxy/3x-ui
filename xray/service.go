package xray

import (
	"fmt"
	"os"
	"strconv"

	"github.com/mhsanaei/3x-ui/v2/logger"
)

type Service struct {
	xrayAPI *XrayAPI
	apiPort int
}

func NewService() (*Service, error) {
	host := resolveAPIHost()
	port := resolveAPIPort()

	api, err := NewXrayAPI(host, port)
	if err != nil {
		logger.Error("Failed to create Xray API client:", err)
		return nil, fmt.Errorf("create Xray API at %s:%d: %w", host, port, err)
	}

	logger.Info("Xray Service initialized with persistent gRPC connection")

	return &Service{
		xrayAPI: api,
		apiPort: port,
	}, nil
}

// resolveAPIHost reads and validates XRAY_HOST from environment.
// Returns validated host or default.
func resolveAPIHost() string {
	const defaultHost = "127.0.0.1"

	host := os.Getenv("XRAY_HOST")
	if host == "" {
		return defaultHost
	}

	// Basic validation - host should not be empty after trim
	if len(host) == 0 {
		logger.Warning("Empty XRAY_HOST value, using default")
		return defaultHost
	}

	return host
}

// resolveAPIPort reads and validates XRAY_PORT from environment.
// Returns validated port or default.
func resolveAPIPort() int {
	const defaultPort = 10085

	portStr := os.Getenv("XRAY_PORT")
	if portStr == "" {
		return defaultPort
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		logger.Warning("Invalid XRAY_PORT value (not a number), using default:", portStr)
		return defaultPort
	}

	if port <= 0 || port > 65535 {
		logger.Warning("Invalid XRAY_PORT value (out of range), using default:", port)
		return defaultPort
	}

	return port
}

func (s *Service) Close() {
	if s != nil && s.xrayAPI != nil {
		s.xrayAPI.Close()
	}
}

func (s *Service) GetTraffic(reset bool) ([]*Traffic, []*ClientTraffic, error) {
	traffic, clientTraffic, err := s.xrayAPI.GetTraffic(reset)
	if err != nil {
		logger.Debug("Failed to fetch Xray traffic:", err)
		return nil, nil, err
	}

	return traffic, clientTraffic, nil
}

func (s *Service) AddInbound(inboundJSON []byte) error {
	if err := s.xrayAPI.AddInbound(inboundJSON); err != nil {
		logger.Error("Failed to add inbound:", err)
		return err
	}

	logger.Debug("Inbound added via gRPC API")
	return nil
}

func (s *Service) DelInbound(tag string) error {
	if err := s.xrayAPI.DelInbound(tag); err != nil {
		logger.Error("Failed to delete inbound:", err)
		return err
	}

	logger.Debug("Inbound deleted via gRPC API:", tag)
	return nil
}

func (s *Service) AddUser(protocol, inboundTag string, user map[string]any) error {
	if err := s.xrayAPI.AddUser(protocol, inboundTag, user); err != nil {
		logger.Error("Failed to add user:", err)
		return err
	}

	email, _ := user["email"].(string)
	if email != "" {
		logger.Debug("User added via gRPC API:", email)
	} else {
		logger.Debug("User added via gRPC API (email not set in user map)")
	}

	return nil
}

func (s *Service) RemoveUser(inboundTag, email string) error {
	if err := s.xrayAPI.RemoveUser(inboundTag, email); err != nil {
		logger.Error("Failed to remove user:", err)
		return err
	}

	logger.Debug("User removed via gRPC API:", email)
	return nil
}
