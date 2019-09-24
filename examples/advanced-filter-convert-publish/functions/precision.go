package functions

import (
	"fmt"
	"strconv"

	"github.com/edgexfoundry/app-functions-sdk-go/appcontext"
	"github.com/edgexfoundry/go-mod-core-contracts/models"
)

func SetPrecision(edgexcontext *appcontext.Context, params ...interface{}) (bool, interface{}) {
	edgexcontext.LoggingClient.Debug("SetPrecision called")

	if len(params) < 1 {
		// We didn't receive a result
		return false, nil
	}

	var err error

	event := params[0].(models.Event)
	for _, reading := range event.Readings {
		if reading.Name == "RandomValue_Int32" {
			precision, err = strconv.Atoi(reading.Value)
			if err != nil {
				edgexcontext.LoggingClient.Error(fmt.Sprintf("unable to convert value '%s' to int: %s", reading.Value, err.Error()))
			} else {
				edgexcontext.LoggingClient.Info(fmt.Sprintf("Float conversion precision has been set to %d", precision))
			}

			return false, nil // Terminates the functions pipeline execution
		}
	}

	return true, event // Continues the functions pipeline execution with the current event
}
