package handler

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"entity-management/internal/auth"
	"entity-management/internal/push"

	"github.com/gin-gonic/gin"
)

// PushHandler serves the Web Push endpoints: the public key of the server,
// the subscriptions of the browser, and the test message.
//
// The endpoints are thin: they read the body, hand it to the push service
// and write the answer. The rules — which account may see what, when a
// subscription dies — live in the push package.
type PushHandler struct {
	push *push.Service
}

func NewPushHandler(service *push.Service) *PushHandler {
	return &PushHandler{push: service}
}

// pushTimeout bounds the work of one push request.
const pushTimeout = 15 * time.Second

// PublicKey answers GET /api/push/vapid-public-key.
//
// The browser needs the public key before it can subscribe. When the server
// has no keys, `enabled` is false and the public key is empty: the feature
// is off, and the frontend says so in plain words.
func (h *PushHandler) PublicKey(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"public_key": h.push.PublicKey(),
		"enabled":    h.push.Enabled(),
	}})
}

// Subscribe answers POST /api/push/subscribe.
//
// The body is the PushSubscription JSON of the browser. The subscription
// belongs to the account that signs the request.
func (h *PushHandler) Subscribe(c *gin.Context) {
	var in push.SubscriptionInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("invalid_json", "request body must be valid JSON"))
		return
	}
	in.Normalize()
	if fields := in.Validate(); len(fields) > 0 {
		c.JSON(http.StatusBadRequest, validationErrorBody(map[string]string(fields)))
		return
	}

	userID, ok := signedInUserID(c)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), pushTimeout)
	defer cancel()

	if err := h.push.Subscribe(ctx, userID, in, c.Request.UserAgent()); err != nil {
		log.Printf("push: subscribe failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{"saved": true}})
}

// unsubscribeInput is the body of DELETE /api/push/subscribe.
type unsubscribeInput struct {
	Endpoint string `json:"endpoint"`
}

// Unsubscribe answers DELETE /api/push/subscribe.
//
// Only the subscription of the signed-in account goes: the endpoint of
// somebody else stays untouched.
func (h *PushHandler) Unsubscribe(c *gin.Context) {
	var in unsubscribeInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("invalid_json", "request body must be valid JSON"))
		return
	}

	endpoint := strings.TrimSpace(in.Endpoint)
	if endpoint == "" {
		c.JSON(http.StatusBadRequest, validationErrorBody(map[string]string{
			"endpoint": "endpoint is required",
		}))
		return
	}

	userID, ok := signedInUserID(c)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), pushTimeout)
	defer cancel()

	if err := h.push.Unsubscribe(ctx, userID, endpoint); err != nil {
		log.Printf("push: unsubscribe failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	c.Status(http.StatusNoContent)
}

// Test answers POST /api/push/test.
//
// It sends a test message to the subscriptions of the caller only, and
// answers how many of them got it. This is the button the operator uses to
// see for himself that the whole chain works.
func (h *PushHandler) Test(c *gin.Context) {
	userID, ok := signedInUserID(c)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), pushTimeout)
	defer cancel()

	sent := h.push.NotifyUser(ctx, userID, push.Payload{
		Title: "Notifikasi tes Groundwork",
		Body:  "Web Push aktif. Kabar unit yang berangkat atau tiba akan muncul di sini.",
		Tag:   "groundwork-push-test",
		URL:   "/",
	})

	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"sent":    sent,
		"enabled": h.push.Enabled(),
	}})
}

// signedInUserID returns the id of the account that made the request.
//
// A token without a readable id is refused: the push belongs to an account,
// and an account it cannot name is not one to send to.
func signedInUserID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(auth.UserID(c), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusUnauthorized, errorBody("unauthorized", "invalid or expired token"))
		return 0, false
	}
	return id, true
}
