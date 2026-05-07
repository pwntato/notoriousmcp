package handlers

import "github.com/pwntato/notoriousmcp/internal/models"

func handleCheckStatus(user *models.User) (*toolsCallResult, *rpcError) {
	switch user.Status {
	case models.StatusPending:
		return textResult("Your account is pending admin approval.")
	default: // banned
		return textResult("Your account has been banned.")
	}
}
