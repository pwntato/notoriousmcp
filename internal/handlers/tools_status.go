package handlers

import (
	"log"

	"github.com/pwntato/notoriousmcp/internal/models"
)

func handleCheckStatus(user *models.User) (*toolsCallResult, *rpcError) {
	switch user.Status {
	case models.StatusPending:
		return textResult("Your account is pending admin approval.")
	case models.StatusBanned:
		return textResult("Your account has been banned.")
	default:
		log.Printf("mcp: check_status called for unexpected status %q (user %s)", user.Status, user.UserID)
		return nil, &rpcError{Code: codeInternalError, Message: "internal error"}
	}
}
