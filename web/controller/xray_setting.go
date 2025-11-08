package controller

import (
	"encoding/json"

	"github.com/mhsanaei/3x-ui/v2/web/service"

	"github.com/gin-gonic/gin"
)

// XraySettingController handles Xray configuration and settings operations.
type XraySettingController struct {
	XraySettingService service.XraySettingService
	SettingService     service.SettingService
	InboundService     service.InboundService
	OutboundService    service.OutboundService
	XrayService        service.XrayService
	WarpService        service.WarpService
}

// NewXraySettingController creates a new XraySettingController and initializes its routes.
func NewXraySettingController(g *gin.RouterGroup) *XraySettingController {
	a := &XraySettingController{}
	a.initRouter(g)
	return a
}

// initRouter sets up the routes for Xray settings management.
func (a *XraySettingController) initRouter(g *gin.RouterGroup) {
	g = g.Group("/xray")
	g.GET("/getDefaultJsonConfig", a.getDefaultXrayConfig)
	g.GET("/getOutboundsTraffic", a.getOutboundsTraffic)
	g.GET("/getXrayResult", a.getXrayResult)

	g.POST("/", a.getXraySetting)
	g.POST("/warp/:action", a.warp)
	g.POST("/update", a.updateSetting)
	g.POST("/resetOutboundsTraffic", a.resetOutboundsTraffic)
}

// getXraySetting retrieves virtual config (template + all outbounds from DB) and inbound tags.
func (a *XraySettingController) getXraySetting(c *gin.Context) {
	virtualConfig, err := a.XrayService.GetVirtualConfigJSON()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.getSettings"), err)
		return
	}
	inboundTags, err := a.InboundService.GetInboundTags()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.getSettings"), err)
		return
	}

	// Convert to JSON string for response
	xraySettingBytes, _ := json.Marshal(virtualConfig)
	inboundTagsBytes, _ := json.Marshal(inboundTags)
	xrayResponse := "{ \"xraySetting\": " + string(xraySettingBytes) + ", \"inboundTags\": " + string(inboundTagsBytes) + " }"
	jsonObj(c, xrayResponse, nil)
}

// updateSetting parses virtual config and updates DB (template + inbounds + outbounds).
func (a *XraySettingController) updateSetting(c *gin.Context) {
	xraySetting := c.PostForm("xraySetting")
	err := a.XrayService.ApplyVirtualConfig(xraySetting)
	jsonMsg(c, I18nWeb(c, "pages.settings.toasts.modifySettings"), err)
}

// getDefaultXrayConfig retrieves full Xray config built from DB (all sections + inbounds + outbounds).
func (a *XraySettingController) getDefaultXrayConfig(c *gin.Context) {
	fullConfig, err := a.XrayService.GetFullXrayConfigFromDB()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.getSettings"), err)
		return
	}
	jsonObj(c, fullConfig, nil)
}

// getXrayResult retrieves the current Xray service result.
func (a *XraySettingController) getXrayResult(c *gin.Context) {
	// Return empty string for backward compatibility
	jsonObj(c, "", nil)
}

// warp handles Warp-related operations based on the action parameter.
func (a *XraySettingController) warp(c *gin.Context) {
	action := c.Param("action")
	var resp string
	var err error
	switch action {
	case "data":
		resp, err = a.WarpService.GetWarpData()
	case "del":
		err = a.WarpService.DelWarpData()
	case "config":
		resp, err = a.WarpService.GetWarpConfig()
	case "reg":
		skey := c.PostForm("privateKey")
		pkey := c.PostForm("publicKey")
		resp, err = a.WarpService.RegWarp(skey, pkey)
	case "license":
		license := c.PostForm("license")
		resp, err = a.WarpService.SetWarpLicense(license)
	}

	jsonObj(c, resp, err)
}

// getOutboundsTraffic retrieves the traffic statistics for outbounds.
func (a *XraySettingController) getOutboundsTraffic(c *gin.Context) {
	outboundsTraffic, err := a.OutboundService.GetOutboundsTraffic()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.getOutboundTrafficError"), err)
		return
	}

	type traffic struct {
		Tag   string `json:"tag"`
		Up    int64  `json:"up"`
		Down  int64  `json:"down"`
		Total int64  `json:"total"`
	}

	result := make([]traffic, 0, len(outboundsTraffic))
	for _, t := range outboundsTraffic {
		if t.Outbound.Tag != "" {
			result = append(result, traffic{
				Tag:   t.Outbound.Tag,
				Up:    t.Up,
				Down:  t.Down,
				Total: t.Total,
			})
		}
	}

	jsonObj(c, result, nil)
}

// resetOutboundsTraffic resets the traffic statistics for the specified outbound tag.
func (a *XraySettingController) resetOutboundsTraffic(c *gin.Context) {
	tag := c.PostForm("tag")
	err := a.OutboundService.ResetOutboundTraffic(tag)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.resetOutboundTrafficError"), err)
		return
	}
	jsonObj(c, "", nil)
}
