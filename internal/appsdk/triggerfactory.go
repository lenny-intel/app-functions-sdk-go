//
// Copyright (c) 2020 Technocrats
// Copyright (c) 2021 Intel Corporation

//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package appsdk

import (
	"errors"
	"fmt"
	"strings"

	"github.com/edgexfoundry/go-mod-messaging/v2/pkg/types"

	"github.com/edgexfoundry/app-functions-sdk-go/v2/internal/appcontext"
	"github.com/edgexfoundry/app-functions-sdk-go/v2/internal/common"
	"github.com/edgexfoundry/app-functions-sdk-go/v2/internal/runtime"
	"github.com/edgexfoundry/app-functions-sdk-go/v2/internal/trigger/http"
	"github.com/edgexfoundry/app-functions-sdk-go/v2/internal/trigger/messagebus"
	"github.com/edgexfoundry/app-functions-sdk-go/v2/internal/trigger/mqtt"
	"github.com/edgexfoundry/app-functions-sdk-go/v2/pkg/interfaces"
)

// RegisterCustomTriggerFactory allows users to register builders for custom trigger types
func (sdk *AppFunctionsSDK) RegisterCustomTriggerFactory(name string,
	factory func(interfaces.TriggerConfig) (interfaces.Trigger, error)) error {
	nu := strings.ToUpper(name)

	if nu == TriggerTypeMessageBus ||
		nu == TriggerTypeHTTP ||
		nu == TriggerTypeMQTT {
		return errors.New(fmt.Sprintf("cannot register custom trigger for builtin type (%s)", name))
	}

	if sdk.customTriggerFactories == nil {
		sdk.customTriggerFactories = make(map[string]func(sdk *AppFunctionsSDK) (interfaces.Trigger, error), 1)
	}

	sdk.customTriggerFactories[nu] = func(sdk *AppFunctionsSDK) (interfaces.Trigger, error) {
		return factory(interfaces.TriggerConfig{
			Config:           sdk.config.Trigger.EdgexMessageBus,
			Logger:           sdk.lc,
			ContextBuilder:   sdk.defaultTriggerContextBuilder,
			MessageProcessor: sdk.defaultTriggerMessageProcessor,
		})
	}

	return nil
}

func (sdk *AppFunctionsSDK) defaultTriggerMessageProcessor(appContext interfaces.AppFunctionContext, envelope types.MessageEnvelope) error {
	context, ok := appContext.(*appcontext.Context)
	if !ok {
		return fmt.Errorf("App Context must be an istance of internal appcontext.Context. Use NewAppContext to create instance.")
	}

	messageError := sdk.runtime.ProcessMessage(context, envelope)
	if messageError != nil {
		// ProcessMessage logs the error, so no need to log it here.
		return messageError.Err
	}

	return nil
}

func (sdk *AppFunctionsSDK) defaultTriggerContextBuilder(env types.MessageEnvelope) interfaces.AppFunctionContext {
	return appcontext.New(env.CorrelationID, sdk.dic, env.ContentType)
}

// setupTrigger configures the appropriate trigger as specified by configuration.
func (sdk *AppFunctionsSDK) setupTrigger(configuration *common.ConfigurationStruct, runtime *runtime.GolangRuntime) interfaces.Trigger {
	var t interfaces.Trigger
	// Need to make dynamic, search for the trigger that is input

	switch triggerType := strings.ToUpper(configuration.Trigger.Type); triggerType {
	case TriggerTypeHTTP:
		sdk.LoggingClient().Info("HTTP trigger selected")
		t = http.NewTrigger(sdk.dic, runtime, sdk.webserver)

	case TriggerTypeMessageBus:
		sdk.LoggingClient().Info("EdgeX MessageBus trigger selected")
		t = messagebus.NewTrigger(sdk.dic, runtime)

	case TriggerTypeMQTT:
		sdk.LoggingClient().Info("External MQTT trigger selected")
		t = mqtt.NewTrigger(sdk.dic, runtime)

	default:
		if factory, found := sdk.customTriggerFactories[triggerType]; found {
			var err error
			t, err = factory(sdk)
			if err != nil {
				sdk.LoggingClient().Error(fmt.Sprintf("failed to initialize custom trigger [%s]: %s", triggerType, err.Error()))
				return nil
			}
		} else {
			sdk.LoggingClient().Error(fmt.Sprintf("Invalid Trigger type of '%s' specified", configuration.Trigger.Type))
		}
	}

	return t
}
