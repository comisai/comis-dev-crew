package application

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// RuntimeRelayIdentityUpgrade binds one legacy preparation to newly durable relay authority.
type RuntimeRelayIdentityUpgrade struct {
	TaskHandle    string
	RelayIdentity string
	RelaySeed     [32]byte `json:"-"`
}

// Validate rejects incomplete or contradictory relay upgrade authority.
func (upgrade RuntimeRelayIdentityUpgrade) Validate() error {
	var nonzero byte
	for _, value := range upgrade.RelaySeed {
		nonzero |= value
	}
	if domain.ValidateTaskHandle(upgrade.TaskHandle) != nil || nonzero == 0 ||
		ValidateRuntimeRelayIdentity(upgrade.RelayIdentity) != nil {
		return errors.New("runtime relay identity upgrade is invalid")
	}
	privateKey := ed25519.NewKeyFromSeed(upgrade.RelaySeed[:])
	identity := hex.EncodeToString(privateKey.Public().(ed25519.PublicKey))
	if identity != upgrade.RelayIdentity {
		return errors.New("runtime relay identity upgrade is contradictory")
	}
	return nil
}
