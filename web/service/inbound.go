// Package service provides business logic services for the 3x-ui web panel,
// including inbound/outbound management, user administration, settings, and Xray integration.
//
// This file serves as a backward-compatibility wrapper.
// The actual implementation has been moved to web/service/inbounds/ package.
package service

import (
	"github.com/mhsanaei/3x-ui/v2/web/service/inbounds"
)

// InboundService is an alias to inbounds.InboundService for backward compatibility.
type InboundService = inbounds.InboundService
