//
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
	"context"
	"errors"
	"fmt"
	nethttp "net/http"
	"os"
	"os/signal"
	"reflect"
	"strings"
	"sync"
	"syscall"

	"github.com/edgexfoundry/go-mod-bootstrap/v2/bootstrap/secret"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/clients/command"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/clients/coredata"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/clients/notifications"
	"github.com/edgexfoundry/go-mod-registry/v2/registry"

	"github.com/edgexfoundry/app-functions-sdk-go/v2/internal"
	"github.com/edgexfoundry/app-functions-sdk-go/v2/internal/bootstrap/container"
	"github.com/edgexfoundry/app-functions-sdk-go/v2/internal/bootstrap/handlers"
	"github.com/edgexfoundry/app-functions-sdk-go/v2/internal/common"
	"github.com/edgexfoundry/app-functions-sdk-go/v2/internal/runtime"
	"github.com/edgexfoundry/app-functions-sdk-go/v2/internal/webserver"
	"github.com/edgexfoundry/app-functions-sdk-go/v2/pkg/interfaces"
	"github.com/edgexfoundry/app-functions-sdk-go/v2/pkg/util"

	"github.com/edgexfoundry/go-mod-bootstrap/v2/bootstrap"
	"github.com/edgexfoundry/go-mod-bootstrap/v2/bootstrap/config"
	bootstrapContainer "github.com/edgexfoundry/go-mod-bootstrap/v2/bootstrap/container"
	"github.com/edgexfoundry/go-mod-bootstrap/v2/bootstrap/flags"
	bootstrapHandlers "github.com/edgexfoundry/go-mod-bootstrap/v2/bootstrap/handlers"
	bootstrapInterfaces "github.com/edgexfoundry/go-mod-bootstrap/v2/bootstrap/interfaces"
	"github.com/edgexfoundry/go-mod-bootstrap/v2/bootstrap/startup"
	"github.com/edgexfoundry/go-mod-bootstrap/v2/di"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/clients"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/clients/logger"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/models"
	"github.com/edgexfoundry/go-mod-messaging/v2/messaging"
	"github.com/edgexfoundry/go-mod-messaging/v2/pkg/types"
	"github.com/gorilla/mux"
)

const (
	// ProfileSuffixPlaceholder is used to create unique names for profiles
	envProfile    = "EDGEX_PROFILE"
	envServiceKey = "EDGEX_SERVICE_KEY"

	TriggerTypeMessageBus = "EDGEX-MESSAGEBUS"
	TriggerTypeMQTT       = "EXTERNAL-MQTT"
	TriggerTypeHTTP       = "HTTP"

	OptionalPasswordKey = "Password"
)

// The key type is unexported to prevent collisions with context keys defined in
// other packages.
type key int

// SDKKey is the context key for getting the sdk context.  Its value of zero is
// arbitrary.  If this package defined other context keys, they would have
// different integer values.
const SDKKey key = 0

func NewSDK(serviceKey string, targetType interface{}, profileSuffixPlaceholder string) *AppFunctionsSDK {
	return &AppFunctionsSDK{
		serviceKey:               serviceKey,
		targetType:               targetType,
		profileSuffixPlaceholder: profileSuffixPlaceholder,
	}
}

type commandLineFlags struct {
	skipVersionCheck   bool
	serviceKeyOverride string
}

type contextGroup struct {
	storeForwardWg        *sync.WaitGroup
	storeForwardCancelCtx context.CancelFunc
	appWg                 *sync.WaitGroup
	appCtx                context.Context
	appCancelCtx          context.CancelFunc
	stop                  context.CancelFunc
}

// AppFunctionsSDK provides the necessary struct to create an instance of the Application Functions SDK. Be sure and provide a ServiceKey
// when creating an instance of the SDK. After creating an instance, you'll first want to call .Initialize(), to start up the SDK. Secondly,
// provide the desired transforms for your pipeline by calling .SetFunctionsPipeline(). Lastly, call .MakeItRun() to start listening for events based on
// your configured trigger.
type AppFunctionsSDK struct {
	dic                       *di.Container
	serviceKey                string
	targetType                interface{}
	config                    *common.ConfigurationStruct
	lc                        logger.LoggingClient
	transforms                []interfaces.AppFunction
	usingConfigurablePipeline bool
	runtime                   *runtime.GolangRuntime
	webserver                 *webserver.WebServer
	ctx                       contextGroup
	deferredFunctions         []bootstrap.Deferred
	backgroundPublishChannel  <-chan types.MessageEnvelope
	customTriggerFactories    map[string]func(sdk *AppFunctionsSDK) (interfaces.Trigger, error)
	profileSuffixPlaceholder  string
	commandLine               commandLineFlags
}

// AddRoute allows you to leverage the existing webserver to add routes.
func (sdk *AppFunctionsSDK) AddRoute(route string, handler func(nethttp.ResponseWriter, *nethttp.Request), methods ...string) error {
	if route == clients.ApiPingRoute ||
		route == clients.ApiConfigRoute ||
		route == clients.ApiMetricsRoute ||
		route == clients.ApiVersionRoute ||
		route == internal.ApiTriggerRoute {
		return errors.New("route is reserved")
	}
	return sdk.webserver.AddRoute(route, sdk.addContext(handler), methods...)
}

// AddBackgroundPublisher will create a channel of provided capacity to be
// consumed by the MessageBus output and return a publisher that writes to it
func (sdk *AppFunctionsSDK) AddBackgroundPublisher(capacity int) interfaces.BackgroundPublisher {
	bgchan, pub := newBackgroundPublisher(capacity)
	sdk.backgroundPublishChannel = bgchan
	return pub
}

// MakeItStop will force the service loop to exit in the same fashion as SIGINT/SIGTERM received from the OS
func (sdk *AppFunctionsSDK) MakeItStop() {
	if sdk.ctx.stop != nil {
		sdk.ctx.stop()
	} else {
		sdk.lc.Warn("MakeItStop called but no stop handler set on SDK - is the service running?")
	}
}

// MakeItRun will initialize and start the trigger as specified in the
// configuration. It will also configure the webserver and start listening on
// the specified port.
func (sdk *AppFunctionsSDK) MakeItRun() error {
	runCtx, stop := context.WithCancel(context.Background())

	sdk.ctx.stop = stop

	sdk.runtime = &runtime.GolangRuntime{
		TargetType: sdk.targetType,
		ServiceKey: sdk.serviceKey,
	}

	sdk.runtime.Initialize(sdk.dic)
	sdk.runtime.SetTransforms(sdk.transforms)

	// determine input type and create trigger for it
	t := sdk.setupTrigger(sdk.config, sdk.runtime)
	if t == nil {
		return errors.New("Failed to create Trigger")
	}

	// Initialize the trigger (i.e. start a web server, or connect to message bus)
	deferred, err := t.Initialize(sdk.ctx.appWg, sdk.ctx.appCtx, sdk.backgroundPublishChannel)
	if err != nil {
		sdk.lc.Error(err.Error())
		return errors.New("Failed to initialize Trigger")
	}

	// deferred is a a function that needs to be called when services exits.
	sdk.addDeferred(deferred)

	if sdk.config.Writable.StoreAndForward.Enabled {
		sdk.startStoreForward()
	} else {
		sdk.lc.Info("StoreAndForward disabled. Not running retry loop.")
	}

	sdk.lc.Info(sdk.config.Service.StartupMsg)

	signals := make(chan os.Signal)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	httpErrors := make(chan error)
	defer close(httpErrors)

	sdk.webserver.StartWebServer(httpErrors)

	select {
	case httpError := <-httpErrors:
		sdk.lc.Info("Http error received: ", httpError.Error())
		err = httpError

	case signalReceived := <-signals:
		sdk.lc.Info("Terminating signal received: " + signalReceived.String())

	case <-runCtx.Done():
		sdk.lc.Info("Terminating: sdk.MakeItStop called")
	}

	sdk.ctx.stop = nil

	if sdk.config.Writable.StoreAndForward.Enabled {
		sdk.ctx.storeForwardCancelCtx()
		sdk.ctx.storeForwardWg.Wait()
	}

	sdk.ctx.appCancelCtx() // Cancel all long running go funcs
	sdk.ctx.appWg.Wait()
	// Call all the deferred funcs that need to happen when exiting.
	// These are things like un-register from the Registry, disconnect from the Message Bus, etc
	for _, deferredFunc := range sdk.deferredFunctions {
		deferredFunc()
	}

	return err
}

// LoadConfigurablePipeline ...
func (sdk *AppFunctionsSDK) LoadConfigurablePipeline() ([]interfaces.AppFunction, error) {
	var pipeline []interfaces.AppFunction

	sdk.usingConfigurablePipeline = true

	sdk.targetType = nil

	if sdk.config.Writable.Pipeline.UseTargetTypeOfByteArray {
		sdk.targetType = &[]byte{}
	}

	configurable := AppFunctionsSDKConfigurable{
		Sdk: sdk,
	}
	valueOfType := reflect.ValueOf(configurable)
	pipelineConfig := sdk.config.Writable.Pipeline
	executionOrder := util.DeleteEmptyAndTrim(strings.FieldsFunc(pipelineConfig.ExecutionOrder, util.SplitComma))

	if len(executionOrder) <= 0 {
		return nil, errors.New(
			"execution Order has 0 functions specified. You must have a least one function in the pipeline")
	}
	sdk.lc.Debugf("Function Pipeline Execution Order: [%s]", pipelineConfig.ExecutionOrder)

	for _, functionName := range executionOrder {
		functionName = strings.TrimSpace(functionName)
		configuration, ok := pipelineConfig.Functions[functionName]
		if !ok {
			return nil, fmt.Errorf("function '%s' configuration not found in Pipeline.Functions section", functionName)
		}

		result := valueOfType.MethodByName(functionName)
		if result.Kind() == reflect.Invalid {
			return nil, fmt.Errorf("function %s is not a built in SDK function", functionName)
		} else if result.IsNil() {
			return nil, fmt.Errorf("invalid/missing configuration for %s", functionName)
		}

		// determine number of parameters required for function call
		inputParameters := make([]reflect.Value, result.Type().NumIn())
		// set keys to be all lowercase to avoid casing issues from configuration
		for key := range configuration.Parameters {
			value := configuration.Parameters[key]
			delete(configuration.Parameters, key) // Make sure the old key has been removed so don't have multiples
			configuration.Parameters[strings.ToLower(key)] = value
		}
		for index := range inputParameters {
			parameter := result.Type().In(index)

			switch parameter {
			case reflect.TypeOf(map[string]string{}):
				inputParameters[index] = reflect.ValueOf(configuration.Parameters)

			default:
				return nil, fmt.Errorf(
					"function %s has an unsupported parameter type: %s",
					functionName,
					parameter.String(),
				)
			}
		}

		function, ok := result.Call(inputParameters)[0].Interface().(interfaces.AppFunction)
		if !ok {
			return nil, fmt.Errorf("failed to cast function %s as AppFunction type", functionName)
		}

		if function == nil {
			return nil, fmt.Errorf("%s from configuration failed", functionName)
		}

		pipeline = append(pipeline, function)
		sdk.lc.Debugf(
			"%s function added to configurable pipeline with parameters: [%s]",
			functionName,
			listParameters(configuration.Parameters))
	}

	return pipeline, nil
}

// SetFunctionsPipeline allows you to define each function to execute and the order in which each function
// will be called as each event comes in.
func (sdk *AppFunctionsSDK) SetFunctionsPipeline(transforms ...interfaces.AppFunction) error {
	if len(transforms) == 0 {
		return errors.New("no transforms provided to pipeline")
	}

	sdk.transforms = transforms

	if sdk.runtime != nil {
		sdk.runtime.SetTransforms(transforms)
		sdk.runtime.TargetType = sdk.targetType
	}

	return nil
}

// ApplicationSettings returns the values specified in the custom configuration section.
func (sdk *AppFunctionsSDK) ApplicationSettings() map[string]string {
	return sdk.config.ApplicationSettings
}

// GetAppSettingStrings returns the strings slice for the specified App Setting.
func (sdk *AppFunctionsSDK) GetAppSettingStrings(setting string) ([]string, error) {
	if sdk.config.ApplicationSettings == nil {
		return nil, fmt.Errorf("%s setting not found: ApplicationSettings section is missing", setting)
	}

	settingValue, ok := sdk.config.ApplicationSettings[setting]
	if !ok {
		return nil, fmt.Errorf("%s setting not found in ApplicationSettings", setting)
	}

	valueStrings := util.DeleteEmptyAndTrim(strings.FieldsFunc(settingValue, util.SplitComma))

	return valueStrings, nil
}

// Initialize will parse command line flags, register for interrupts,
// initialize the logging system, and ingest configuration.
func (sdk *AppFunctionsSDK) Initialize() error {
	startupTimer := startup.NewStartUpTimer(sdk.serviceKey)

	additionalUsage :=
		"    -s/--skipVersionCheck           Indicates the service should skip the Core Service's version compatibility check.\n" +
			"    -sk/--serviceKey                Overrides the service service key used with Registry and/or Configuration Providers.\n" +
			"                                    If the name provided contains the text `<profile>`, this text will be replaced with\n" +
			"                                    the name of the profile used."

	sdkFlags := flags.NewWithUsage(additionalUsage)
	sdkFlags.FlagSet.BoolVar(&sdk.commandLine.skipVersionCheck, "skipVersionCheck", false, "")
	sdkFlags.FlagSet.BoolVar(&sdk.commandLine.skipVersionCheck, "s", false, "")
	sdkFlags.FlagSet.StringVar(&sdk.commandLine.serviceKeyOverride, "serviceKey", "", "")
	sdkFlags.FlagSet.StringVar(&sdk.commandLine.serviceKeyOverride, "sk", "", "")

	sdkFlags.Parse(os.Args[1:])

	// Temporarily setup logging to STDOUT so the client can be used before bootstrapping is completed
	sdk.lc = logger.NewClient(sdk.serviceKey, models.InfoLog)

	sdk.setServiceKey(sdkFlags.Profile())

	sdk.lc.Info(fmt.Sprintf("Starting %s %s ", sdk.serviceKey, internal.ApplicationVersion))

	sdk.config = &common.ConfigurationStruct{}
	sdk.dic = di.NewContainer(di.ServiceConstructorMap{
		container.ConfigurationName: func(get di.Get) interface{} {
			return sdk.config
		},
	})

	sdk.ctx.appCtx, sdk.ctx.appCancelCtx = context.WithCancel(context.Background())
	sdk.ctx.appWg = &sync.WaitGroup{}

	var deferred bootstrap.Deferred
	var successful bool
	var configUpdated config.UpdatedStream = make(chan struct{})

	sdk.ctx.appWg, deferred, successful = bootstrap.RunAndReturnWaitGroup(
		sdk.ctx.appCtx,
		sdk.ctx.appCancelCtx,
		sdkFlags,
		sdk.serviceKey,
		internal.ConfigRegistryStem,
		sdk.config,
		configUpdated,
		startupTimer,
		sdk.dic,
		[]bootstrapInterfaces.BootstrapHandler{
			bootstrapHandlers.SecureProviderBootstrapHandler,
			handlers.NewDatabase().BootstrapHandler,
			handlers.NewClients().BootstrapHandler,
			handlers.NewTelemetry().BootstrapHandler,
			handlers.NewVersionValidator(sdk.commandLine.skipVersionCheck, internal.SDKVersion).BootstrapHandler,
		},
	)

	// deferred is a a function that needs to be called when services exits.
	sdk.addDeferred(deferred)

	if !successful {
		return fmt.Errorf("boostrapping failed")
	}

	// Bootstrapping is complete, so now need to retrieve the needed objects from the containers.
	sdk.lc = bootstrapContainer.LoggingClientFrom(sdk.dic.Get)

	// If using the RedisStreams MessageBus implementation then need to make sure the
	// password for the Redis DB is set in the MessageBus Optional properties.
	triggerType := strings.ToUpper(sdk.config.Trigger.Type)
	if triggerType == TriggerTypeMessageBus &&
		sdk.config.Trigger.EdgexMessageBus.Type == messaging.RedisStreams {

		secretProvider := bootstrapContainer.SecretProviderFrom(sdk.dic.Get)
		credentials, err := secretProvider.GetSecrets(sdk.config.Database.Type)
		if err != nil {
			return fmt.Errorf("unable to set RedisStreams password from DB credentials")
		}
		sdk.config.Trigger.EdgexMessageBus.Optional[OptionalPasswordKey] = credentials[secret.PasswordKey]
	}

	// We do special processing when the writeable section of the configuration changes, so have
	// to wait to be signaled when the configuration has been updated and then process the changes
	NewConfigUpdateProcessor(sdk).WaitForConfigUpdates(configUpdated)

	sdk.webserver = webserver.NewWebServer(sdk.dic, mux.NewRouter())
	sdk.webserver.ConfigureStandardRoutes()

	sdk.lc.Info("Service started in: " + startupTimer.SinceAsString())

	return nil
}

// GetSecret retrieves secret data from the secret store at the specified path.
func (sdk *AppFunctionsSDK) GetSecret(path string, keys ...string) (map[string]string, error) {
	secretProvider := bootstrapContainer.SecretProviderFrom(sdk.dic.Get)
	return secretProvider.GetSecrets(path, keys...)
}

// StoreSecret stores the secret data to a secret store at the specified path.
func (sdk *AppFunctionsSDK) StoreSecret(path string, secretData map[string]string) error {
	secretProvider := bootstrapContainer.SecretProviderFrom(sdk.dic.Get)
	return secretProvider.StoreSecrets(path, secretData)
}

func (sdk *AppFunctionsSDK) LoggingClient() logger.LoggingClient {
	return sdk.lc
}

func (sdk *AppFunctionsSDK) RegistryClient() registry.Client {
	return bootstrapContainer.RegistryFrom(sdk.dic.Get)
}

func (sdk *AppFunctionsSDK) EventClient() coredata.EventClient {
	return container.EventClientFrom(sdk.dic.Get)
}

func (sdk *AppFunctionsSDK) CommandClient() command.CommandClient {
	return container.CommandClientFrom(sdk.dic.Get)
}

func (sdk *AppFunctionsSDK) NotificationsClient() notifications.NotificationsClient {
	return container.NotificationsClientFrom(sdk.dic.Get)
}

func listParameters(parameters map[string]string) string {
	result := ""
	first := true
	for key, value := range parameters {
		if first {
			result = fmt.Sprintf("%s='%s'", key, value)
			first = false
			continue
		}

		result += fmt.Sprintf(", %s='%s'", key, value)
	}

	return result
}

func (sdk *AppFunctionsSDK) addContext(next func(nethttp.ResponseWriter, *nethttp.Request)) func(nethttp.ResponseWriter, *nethttp.Request) {
	return func(w nethttp.ResponseWriter, r *nethttp.Request) {
		ctx := context.WithValue(r.Context(), SDKKey, sdk)
		next(w, r.WithContext(ctx))
	}
}

func (sdk *AppFunctionsSDK) addDeferred(deferred bootstrap.Deferred) {
	if deferred != nil {
		sdk.deferredFunctions = append(sdk.deferredFunctions, deferred)
	}
}

// setServiceKey creates the service's service key with profile name if the original service key has the
// appropriate profile placeholder, otherwise it leaves the original service key unchanged
func (sdk *AppFunctionsSDK) setServiceKey(profile string) {
	envValue := os.Getenv(envServiceKey)
	if len(envValue) > 0 {
		sdk.commandLine.serviceKeyOverride = envValue
		sdk.lc.Info(
			fmt.Sprintf("Environment profileOverride of '-n/--serviceName' by environment variable: %s=%s",
				envServiceKey,
				envValue))
	}

	// serviceKeyOverride may have been set by the -n/--serviceName command-line option and not the environment variable
	if len(sdk.commandLine.serviceKeyOverride) > 0 {
		sdk.serviceKey = sdk.commandLine.serviceKeyOverride
	}

	if !strings.Contains(sdk.serviceKey, sdk.profileSuffixPlaceholder) {
		// No placeholder, so nothing to do here
		return
	}

	// Have to handle environment override here before common bootstrap is used so it is passed the proper service key
	profileOverride := os.Getenv(envProfile)
	if len(profileOverride) > 0 {
		profile = profileOverride
	}

	if len(profile) > 0 {
		sdk.serviceKey = strings.Replace(sdk.serviceKey, sdk.profileSuffixPlaceholder, profile, 1)
		return
	}

	// No profile specified so remove the placeholder text
	sdk.serviceKey = strings.Replace(sdk.serviceKey, sdk.profileSuffixPlaceholder, "", 1)
}
