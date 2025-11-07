package job

import (
	"encoding/json"

	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/web/service"
	"github.com/mhsanaei/3x-ui/v2/xray"

	"github.com/valyala/fasthttp"
)

// XrayTrafficJob collects and processes traffic statistics from Xray.
type XrayTrafficJob struct {
	settingService  service.SettingService
	xrayService     *service.XrayService
	inboundService  service.InboundService
	outboundService service.OutboundService
}

// NewXrayTrafficJob creates a new traffic collection job instance.
func NewXrayTrafficJob(xrayService *service.XrayService) *XrayTrafficJob {
	return &XrayTrafficJob{
		xrayService: xrayService,
	}
}

// Run collects traffic statistics from Xray and updates the database.
func (j *XrayTrafficJob) Run() {
	traffics, clientTraffics, err := j.xrayService.GetXrayTraffic()
	if err != nil {
		return
	}

	// Update inbound traffic in database
	err, _ = j.inboundService.AddTraffic(traffics, clientTraffics)
	if err != nil {
		logger.Warning("add inbound traffic failed:", err)
	}

	// Update outbound traffic in database
	err, _ = j.outboundService.AddTraffic(traffics, clientTraffics)
	if err != nil {
		logger.Warning("add outbound traffic failed:", err)
	}

	// Send traffic to external API if enabled
	if externalEnabled, err := j.settingService.GetExternalTrafficInformEnable(); externalEnabled {
		j.informTrafficToExternalAPI(traffics, clientTraffics)
	} else if err != nil {
		logger.Warning("get ExternalTrafficInformEnable failed:", err)
	}
}

func (j *XrayTrafficJob) informTrafficToExternalAPI(inboundTraffics []*xray.Traffic, clientTraffics []*xray.ClientTraffic) {
	informURL, err := j.settingService.GetExternalTrafficInformURI()
	if err != nil {
		logger.Warning("get ExternalTrafficInformURI failed:", err)
		return
	}
	requestBody, err := json.Marshal(map[string]any{"clientTraffics": clientTraffics, "inboundTraffics": inboundTraffics})
	if err != nil {
		logger.Warning("parse client/inbound traffic failed:", err)
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
	if err := fasthttp.Do(request, response); err != nil {
		logger.Warning("POST ExternalTrafficInformURI failed:", err)
	}
}
