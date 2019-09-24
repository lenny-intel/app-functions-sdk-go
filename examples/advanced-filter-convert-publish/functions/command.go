package functions

import (
	"context"
	"fmt"
	"github.com/edgexfoundry/app-functions-sdk-go/appcontext"
	"github.com/edgexfoundry/go-mod-core-contracts/models"
)

const targetFloatCount = 4

var floatCountDown = targetFloatCount

func SendInt32Command(edgexcontext *appcontext.Context, params ...interface{}) (bool, interface{}) {
	edgexcontext.LoggingClient.Debug("SendInt32Command called")

	if len(params) < 1 {
		// We didn't receive a result
		return false, nil
	}

	event := params[0].(models.Event)

	if event.Device == "Random-Float-Device" {
		floatCountDown--
		if floatCountDown == 0 {
			floatCountDown = targetFloatCount
			sendCommand(edgexcontext)
			edgexcontext.LoggingClient.Debug("Int32 command sent")

		}
	}

	return true, event // Continues the functions pipeline execution with the current event
}

func sendCommand(edgexcontext *appcontext.Context) {
	device, ok := edgexcontext.Configuration.ApplicationSettings["CommandDevice"]
	if !ok {
		edgexcontext.LoggingClient.Error("CommandDevice Application setting not found")
		return
	}

	command, ok := edgexcontext.Configuration.ApplicationSettings["CommandName"]
	if !ok {
		edgexcontext.LoggingClient.Error("CommandName Application setting not found")
		return
	}

	_, err := edgexcontext.CommandClient.GetDeviceCommandByNames(device, command, context.Background())
	if err != nil {
		edgexcontext.LoggingClient.Error(fmt.Sprintf("Error sending '%s' command: %s", command, err.Error()))
		return
	}
}
