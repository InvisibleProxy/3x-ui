// Package inbounds provides business logic for managing Xray inbound configurations.
// It handles CRUD operations for inbounds, client management, traffic monitoring,
// and integration with the Xray API for real-time updates.
package inbounds

import (
	"github.com/mhsanaei/3x-ui/v2/xray"
)

// onlineClients stores the list of currently online client emails.
var onlineClients []string

// InboundService provides business logic for managing Xray inbound configurations.
// It handles CRUD operations for inbounds, client management, traffic monitoring,
// and integration with the Xray API for real-time updates.
type InboundService struct {
	xrayApi *xray.XrayAPI
}

// SetXrayAPI sets the XrayAPI instance for this service.
func (s *InboundService) SetXrayAPI(api *xray.XrayAPI) {
	s.xrayApi = api
}

// GetOnlineClients returns the list of currently online client emails.
func (s *InboundService) GetOnlineClients() []string {
	return onlineClients
}
