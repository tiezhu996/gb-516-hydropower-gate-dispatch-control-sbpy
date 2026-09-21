package handler

import (
	"net/http"

	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/dto"
	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/middleware"
	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/service"
	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/util"
	"github.com/gin-gonic/gin"
)

type DispatchPermitHandler struct {
	service service.DispatchPermitService
}

func NewDispatchPermitHandler(s service.DispatchPermitService) *DispatchPermitHandler {
	return &DispatchPermitHandler{service: s}
}

func (h *DispatchPermitHandler) Register(group *gin.RouterGroup) {
	resource := group.Group("/permits")
	resource.GET("", h.list)
	resource.GET("/:id", h.get)
	resource.POST("", middleware.RequireRoles("operator", "admin"), h.apply)
	resource.POST("/:id/approve", middleware.RequireRoles("reviewer", "admin"), h.approve)
	resource.POST("/:id/reject", middleware.RequireRoles("reviewer", "admin"), h.reject)
	resource.POST("/:id/activate", middleware.RequireRoles("operator", "admin"), h.activate)
	resource.POST("/:id/invalidate", middleware.RequireRoles("operator", "reviewer", "admin"), h.invalidate)
}

func (h *DispatchPermitHandler) list(c *gin.Context) {
	query := bindPage(c)
	result, err := h.service.List(c.Request.Context(), query)
	if err != nil {
		handleError(c, err)
		return
	}
	util.Page(c, result.Items, result.Page, result.PageSize, result.Total)
}

func (h *DispatchPermitHandler) get(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	item, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		handleError(c, err)
		return
	}
	util.OK(c, item)
}

func (h *DispatchPermitHandler) apply(c *gin.Context) {
	var input dto.CreateDispatchPermit
	if err := c.ShouldBindJSON(&input); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	item, err := h.service.Apply(c.Request.Context(), input, actorFromContext(c), requestIDFromContext(c))
	if err != nil {
		handleError(c, err)
		return
	}
	util.Created(c, item)
}

func (h *DispatchPermitHandler) approve(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var input dto.PermitReviewRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	item, err := h.service.Approve(c.Request.Context(), id, input, actorFromContext(c), roleFromContext(c), requestIDFromContext(c))
	if err != nil {
		handleError(c, err)
		return
	}
	util.OK(c, item)
}

func (h *DispatchPermitHandler) reject(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var input dto.PermitReviewRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	item, err := h.service.Reject(c.Request.Context(), id, input, actorFromContext(c), roleFromContext(c), requestIDFromContext(c))
	if err != nil {
		handleError(c, err)
		return
	}
	util.OK(c, item)
}

func (h *DispatchPermitHandler) activate(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var input dto.PermitActivationRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	item, err := h.service.Activate(c.Request.Context(), id, input, actorFromContext(c), roleFromContext(c), requestIDFromContext(c))
	if err != nil {
		handleError(c, err)
		return
	}
	util.OK(c, item)
}

func (h *DispatchPermitHandler) invalidate(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var input dto.PermitInvalidateRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	item, err := h.service.Invalidate(c.Request.Context(), id, input, actorFromContext(c), roleFromContext(c), requestIDFromContext(c))
	if err != nil {
		handleError(c, err)
		return
	}
	util.OK(c, item)
}
