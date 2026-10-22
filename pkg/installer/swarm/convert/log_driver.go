package convert

import (
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/moby/moby/api/types/swarm"
)

func logDriver(logging *types.LoggingConfig) *swarm.Driver {
	if logging == nil {
		return nil
	}
	return &swarm.Driver{Name: logging.Driver, Options: logging.Options}
}
