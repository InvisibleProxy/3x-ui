package job

import (
	"encoding/json"

	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/web/service"
	"github.com/mhsanaei/3x-ui/v2/xray"

	"github.com/valyala/fasthttp"
)

type CollectTraffic struct {
	settingService  service.SettingService
	xrayService     *service.XrayService
	inboundService  service.InboundService
	outboundService service.OutboundService
}

func NewCollectTraffic(xrayService *service.XrayService) *CollectTraffic {
	job := &CollectTraffic{
		xrayService: xrayService,
	}

	if xrayService != nil && xrayService.GetXrayAPI() != nil {
		job.inboundService.SetXrayAPI(xrayService.GetXrayAPI())
		job.outboundService.SetXrayAPI(xrayService.GetXrayAPI())
	}

	return job
}

// Run collects traffic statistics from Xray and updates DB.
// Called every 10 seconds for traffic monitoring.
//
// Business logic:
// 1. Gets traffic statistics from Xray (inbounds + clients)
// 2. Updates statistics in DB via inboundService.ProcessTrafficStats:
//   - Updates traffic counters for inbounds and clients
//   - Auto-renews clients (if configured)
//   - Disables clients with exceeded limits
//   - Disables inbounds with exceeded limits
//
// 3. Updates outbound statistics via outboundService.AddTraffic
// 4. Sends statistics to external API (if configured)
func (j *CollectTraffic) Run() {
	traffics, clientTraffics, err := j.xrayService.GetTraffic()
	if err != nil {
		logger.Debug("Failed to get Xray traffic stats:", err)
		return
	}

	err = j.inboundService.ProcessTrafficStats(traffics, clientTraffics)
	if err != nil {
		logger.Warning("Failed to update inbound traffic:", err)
	}

	err, _ = j.outboundService.AddTraffic(traffics, clientTraffics)
	if err != nil {
		logger.Warning("Failed to update outbound traffic:", err)
	}

	if externalEnabled, err := j.settingService.GetExternalTrafficInformEnable(); externalEnabled {
		j.informTrafficToExternalAPI(traffics, clientTraffics)
	} else if err != nil {
		logger.Warning("Failed to get ExternalTrafficInformEnable:", err)
	}
}

// informTrafficToExternalAPI sends traffic statistics to external API.
// Used for integration with external monitoring systems.
func (j *CollectTraffic) informTrafficToExternalAPI(inboundTraffics []*xray.Traffic, clientTraffics []*xray.ClientTraffic) {
	informURL, err := j.settingService.GetExternalTrafficInformURI()
	if err != nil {
		return
	}
	requestBody, err := json.Marshal(map[string]any{"clientTraffics": clientTraffics, "inboundTraffics": inboundTraffics})
	if err != nil {
		return
	}
	request := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(request)
	request.Header.SetMethod("POST")
	request.Header.SetContentType("application/json; charset=UTF-8")
	request.SetBody([]byte(requestBody))
	request.SetRequestURI(informURL)
	response := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(response)
	fasthttp.Do(request, response)
}
