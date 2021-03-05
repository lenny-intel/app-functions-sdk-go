//
// Copyright (c) 2020 Intel Corporation
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
	"io/ioutil"
	nethttp "net/http"
	"os"
	"os/signal"
	"reflect"
	"strings"
	"sync"
	"syscall"

	"github.com/edgexfoundry/go-mod-configuration/v2/configuration"
	"github.com/pelletier/go-toml"

	"github.com/edgexfoundry/app-functions-sdk-go/v2/appcontext"
	"github.com/edgexfoundry/app-functions-sdk-go/v2/internal"
	"github.com/edgexfoundry/app-functions-sdk-go/v2/internal/bootstrap/container"
	"github.com/edgexfoundry/app-functions-sdk-go/v2/internal/bootstrap/handlers"
	"github.com/edgexfoundry/app-functions-sdk-go/v2/internal/common"
	"github.com/edgexfoundry/app-functions-sdk-go/v2/internal/runtime"
	"github.com/edgexfoundry/app-functions-sdk-go/v2/internal/store/db/interfaces"
	"github.com/edgexfoundry/app-functions-sdk-go/v2/internal/webserver"
	"github.com/edgexfoundry/app-functions-sdk-go/v2/pkg/util"

	"github.com/edgexfoundry/go-mod-bootstrap/v2/bootstrap"
	"github.com/edgexfoundry/go-mod-bootstrap/v2/bootstrap/config"
	bootstrapContainer "github.com/edgexfoundry/go-mod-bootstrap/v2/bootstrap/container"
	"github.com/edgexfoundry/go-mod-bootstrap/v2/bootstrap/flags"
	bootstrapHandlers "github.com/edgexfoundry/go-mod-bootstrap/v2/bootstrap/handlers"
	bootstrapInterfaces "github.com/edgexfoundry/go-mod-bootstrap/v2/bootstrap/interfaces"
	"github.com/edgexfoundry/go-mod-bootstrap/v2/bootstrap/secret"
	"github.com/edgexfoundry/go-mod-bootstrap/v2/bootstrap/startup"
	"github.com/edgexfoundry/go-mod-bootstrap/v2/di"
	"github.com/edgexfoundry/go-mod-messaging/v2/messaging"
	"github.com/edgexfoundry/go-mod-messaging/v2/pkg/types"
	"github.com/edgexfoundry/go-mod-registry/v2/registry"

	"github.com/edgexfoundry/go-mod-core-contracts/v2/clients"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/clients/logger"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/models"

	"github.com/gorilla/mux"
)

const (
	// ProfileSuffixPlaceholder is used to create unique names for profiles
	ProfileSuffixPlaceholder = "<profile>"
	envProfile               = "EDGEX_PROFILE"
	envServiceKey            = "EDGEX_SERVICE_KEY"

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

// AppFunctionsSDK provides the necessary struct to create an instance of the Application Functions SDK. Be sure and provide a ServiceKey
// when creating an instance of the SDK. After creating an instance, you'll first want to call .Initialize(), to start up the SDK. Secondly,
// provide the desired transforms for your pipeline by calling .SetFunctionsPipeline(). Lastly, call .MakeItRun() to start listening for events based on
// your configured trigger.
type AppFunctionsSDK struct {
	// ServiceKey is the application services' key used for Configuration and Registration when the Registry is enabled
	ServiceKey string
	// LoggingClient is the EdgeX logger client used to log messages
	LoggingClient logger.LoggingClient
	// TargetType is the expected type of the incoming data. Must be set to a pointer to an instance of the type.
	// Defaults to &models.Event{} if nil. The income data is un-marshaled (JSON or CBOR) in to the type,
	// except when &[]byte{} is specified. In this case the []byte data is pass to the first function in the Pipeline.
	TargetType interface{}
	// EdgexClients allows access to the EdgeX clients such as the CommandClient.
	// Note that the individual clients (e.g EdgexClients.CommandClient) may be nil if the Service (Command) is not configured
	// in the [Clients] section of the App Service's configuration.
	// It is highly recommend that the clients are verified to not be nil before use.
	EdgexClients common.EdgeXClients
	// RegistryClient is the client used by service to communicate with service registry.
	RegistryClient            registry.Client
	ConfigClient              configuration.Client
	transforms                []appcontext.AppFunction
	skipVersionCheck          bool
	usingConfigurablePipeline bool
	httpErrors                chan error
	runtime                   *runtime.GolangRuntime
	webserver                 *webserver.WebServer
	config                    *common.ConfigurationStruct
	CustomConfig              interface{}
	storeClient               interfaces.StoreClient
	secretProvider            bootstrapInterfaces.SecretProvider
	storeForwardWg            *sync.WaitGroup
	storeForwardCancelCtx     context.CancelFunc
	appWg                     *sync.WaitGroup
	appCtx                    context.Context
	appCancelCtx              context.CancelFunc
	deferredFunctions         []bootstrap.Deferred
	serviceKeyOverride        string
	backgroundChannel         <-chan types.MessageEnvelope
	customTriggerFactories    map[string]func(sdk *AppFunctionsSDK) (Trigger, error)
	stop                      context.CancelFunc
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
func (sdk *AppFunctionsSDK) AddBackgroundPublisher(capacity int) BackgroundPublisher {
	bgchan, pub := newBackgroundPublisher(capacity)
	sdk.backgroundChannel = bgchan
	return pub
}

// MakeItStop will force the service loop to exit in the same fashion as SIGINT/SIGTERM received from the OS
func (sdk *AppFunctionsSDK) MakeItStop() {
	if sdk.stop != nil {
		sdk.stop()
	} else {
		sdk.LoggingClient.Warn("MakeItStop called but no stop handler set on SDK - is the service running?")
	}
}

// MakeItRun will initialize and start the trigger as specified in the
// configuration. It will also configure the webserver and start listening on
// the specified port.
func (sdk *AppFunctionsSDK) MakeItRun() error {
	runCtx, stop := context.WithCancel(context.Background())

	sdk.stop = stop

	httpErrors := make(chan error)
	defer close(httpErrors)

	sdk.runtime = &runtime.GolangRuntime{
		TargetType: sdk.TargetType,
		ServiceKey: sdk.ServiceKey,
	}

	sdk.runtime.Initialize(sdk.storeClient, sdk.secretProvider)
	sdk.runtime.SetTransforms(sdk.transforms)

	// determine input type and create trigger for it
	t := sdk.setupTrigger(sdk.config, sdk.runtime)
	if t == nil {
		return errors.New("Failed to create Trigger")
	}

	// Initialize the trigger (i.e. start a web server, or connect to message bus)
	deferred, err := t.Initialize(sdk.appWg, sdk.appCtx, sdk.backgroundChannel)
	if err != nil {
		sdk.LoggingClient.Error(err.Error())
		return errors.New("Failed to initialize Trigger")
	}

	// deferred is a a function that needs to be called when services exits.
	sdk.addDeferred(deferred)

	if sdk.config.Writable.StoreAndForward.Enabled {
		sdk.startStoreForward()
	} else {
		sdk.LoggingClient.Info("StoreAndForward disabled. Not running retry loop.")
	}

	sdk.LoggingClient.Info(sdk.config.Service.StartupMsg)

	signals := make(chan os.Signal)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	sdk.webserver.StartWebServer(sdk.httpErrors)

	select {
	case httpError := <-sdk.httpErrors:
		sdk.LoggingClient.Info("Http error received: ", httpError.Error())
		err = httpError

	case signalReceived := <-signals:
		sdk.LoggingClient.Info("Terminating signal received: " + signalReceived.String())

	case <-runCtx.Done():
		sdk.LoggingClient.Info("Terminating: sdk.MakeItStop called")
	}

	sdk.stop = nil

	if sdk.config.Writable.StoreAndForward.Enabled {
		sdk.storeForwardCancelCtx()
		sdk.storeForwardWg.Wait()
	}

	sdk.appCancelCtx() // Cancel all long running go funcs
	sdk.appWg.Wait()
	// Call all the deferred funcs that need to happen when exiting.
	// These are things like un-register from the Registry, disconnect from the Message Bus, etc
	for _, deferredFunc := range sdk.deferredFunctions {
		deferredFunc()
	}

	return err
}

// LoadConfigurablePipeline ...
func (sdk *AppFunctionsSDK) LoadConfigurablePipeline() ([]appcontext.AppFunction, error) {
	var pipeline []appcontext.AppFunction

	sdk.usingConfigurablePipeline = true

	sdk.TargetType = nil

	if sdk.config.Writable.Pipeline.UseTargetTypeOfByteArray {
		sdk.TargetType = &[]byte{}
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
	sdk.LoggingClient.Debugf("Function Pipeline Execution Order: [%s]", pipelineConfig.ExecutionOrder)

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

		function, ok := result.Call(inputParameters)[0].Interface().(appcontext.AppFunction)
		if !ok {
			return nil, fmt.Errorf("failed to cast function %s as AppFunction type", functionName)
		}

		if function == nil {
			return nil, fmt.Errorf("%s from configuration failed", functionName)
		}

		pipeline = append(pipeline, function)
		configurable.Sdk.LoggingClient.Debugf(
			"%s function added to configurable pipeline with parameters: [%s]",
			functionName,
			listParameters(configuration.Parameters))
	}

	return pipeline, nil
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

// SetFunctionsPipeline allows you to define each function to execute and the order in which each function
// will be called as each event comes in.
func (sdk *AppFunctionsSDK) SetFunctionsPipeline(transforms ...appcontext.AppFunction) error {
	if len(transforms) == 0 {
		return errors.New("no transforms provided to pipeline")
	}

	sdk.transforms = transforms

	if sdk.runtime != nil {
		sdk.runtime.SetTransforms(transforms)
		sdk.runtime.TargetType = sdk.TargetType
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
	startupTimer := startup.NewStartUpTimer(sdk.ServiceKey)

	additionalUsage :=
		"    -s/--skipVersionCheck           Indicates the service should skip the Core Service's version compatibility check.\n" +
			"    -sk/--serviceKey                Overrides the service service key used with Registry and/or Configuration Providers.\n" +
			"                                    If the name provided contains the text `<profile>`, this text will be replaced with\n" +
			"                                    the name of the profile used."

	sdkFlags := flags.NewWithUsage(additionalUsage)
	sdkFlags.FlagSet.BoolVar(&sdk.skipVersionCheck, "skipVersionCheck", false, "")
	sdkFlags.FlagSet.BoolVar(&sdk.skipVersionCheck, "s", false, "")
	sdkFlags.FlagSet.StringVar(&sdk.serviceKeyOverride, "serviceKey", "", "")
	sdkFlags.FlagSet.StringVar(&sdk.serviceKeyOverride, "sk", "", "")

	sdkFlags.Parse(os.Args[1:])

	// Temporarily setup logging to STDOUT so the client can be used before bootstrapping is completed
	sdk.LoggingClient = logger.NewClient(sdk.ServiceKey, models.InfoLog)

	sdk.setServiceKey(sdkFlags.Profile())

	sdk.LoggingClient.Info(fmt.Sprintf("Starting %s %s ", sdk.ServiceKey, internal.ApplicationVersion))

	sdk.config = &common.ConfigurationStruct{}

	dic := di.NewContainer(di.ServiceConstructorMap{
		container.ConfigurationName: func(get di.Get) interface{} {
			return sdk.config
		},
	})

	sdk.appCtx, sdk.appCancelCtx = context.WithCancel(context.Background())
	sdk.appWg = &sync.WaitGroup{}

	var deferred bootstrap.Deferred
	var successful bool
	var configUpdated config.UpdatedStream = make(chan struct{})

	sdk.appWg, deferred, successful = bootstrap.RunAndReturnWaitGroup(
		sdk.appCtx,
		sdk.appCancelCtx,
		sdkFlags,
		sdk.ServiceKey,
		internal.ConfigRegistryStem,
		sdk.config,
		configUpdated,
		startupTimer,
		dic,
		[]bootstrapInterfaces.BootstrapHandler{
			bootstrapHandlers.SecureProviderBootstrapHandler,
			handlers.NewDatabase().BootstrapHandler,
			handlers.NewClients().BootstrapHandler,
			handlers.NewTelemetry().BootstrapHandler,
			handlers.NewVersionValidator(sdk.skipVersionCheck, internal.SDKVersion).BootstrapHandler,
		},
	)

	// deferred is a a function that needs to be called when services exits.
	sdk.addDeferred(deferred)

	if !successful {
		return fmt.Errorf("boostrapping failed")
	}

	if sdk.CustomConfig != nil {
		contents, err := ioutil.ReadFile("./res/configuration.toml")
		if err != nil {
			return fmt.Errorf("Failed to load custom configuration: %s", err.Error())
		}

		err = toml.Unmarshal(contents, sdk.CustomConfig)
		if err != nil {
			return fmt.Errorf("Still unable to unmarshel custom toml config: %s", err.Error())

		}

		sdk.LoggingClient.Infof("Custom config is: %v", sdk.CustomConfig)

		configClient := bootstrapContainer.ConfigClientFrom(dic.Get)
		sdk.LoggingClient.Infof("using config client is %v", configClient != nil)
		if configClient != nil {
			err = configClient.PutConfiguration(reflect.ValueOf(sdk.CustomConfig).Elem().Interface(), true)
			if err != nil {
				return fmt.Errorf("error pushing custom config to Consul: %s", err.Error())
			}

			sdk.LoggingClient.Info("Config pushed to Consul")

		}
	}

	// Bootstrapping is complete, so now need to retrieve the needed objects from the containers.
	sdk.secretProvider = bootstrapContainer.SecretProviderFrom(dic.Get)
	sdk.storeClient = container.StoreClientFrom(dic.Get)
	sdk.LoggingClient = bootstrapContainer.LoggingClientFrom(dic.Get)
	sdk.RegistryClient = bootstrapContainer.RegistryFrom(dic.Get)
	sdk.EdgexClients.LoggingClient = sdk.LoggingClient
	sdk.EdgexClients.EventClient = container.EventClientFrom(dic.Get)
	sdk.EdgexClients.ValueDescriptorClient = container.ValueDescriptorClientFrom(dic.Get)
	sdk.EdgexClients.NotificationsClient = container.NotificationsClientFrom(dic.Get)
	sdk.EdgexClients.CommandClient = container.CommandClientFrom(dic.Get)

	// If using the RedisStreams MessageBus implementation then need to make sure the
	// password for the Redis DB is set in the MessageBus Optional properties.
	triggerType := strings.ToUpper(sdk.config.Trigger.Type)
	if triggerType == TriggerTypeMessageBus &&
		sdk.config.Trigger.EdgexMessageBus.Type == messaging.RedisStreams {

		credentials, err := sdk.secretProvider.GetSecrets(sdk.config.Database.Type)
		if err != nil {
			return fmt.Errorf("unable to set RedisStreams password from DB credentials")
		}
		sdk.config.Trigger.EdgexMessageBus.Optional[OptionalPasswordKey] = credentials[secret.PasswordKey]
	}

	// We do special processing when the writeable section of the configuration changes, so have
	// to wait to be signaled when the configuration has been updated and then process the changes
	NewConfigUpdateProcessor(sdk).WaitForConfigUpdates(configUpdated)

	sdk.webserver = webserver.NewWebServer(sdk.config, sdk.secretProvider, sdk.LoggingClient, mux.NewRouter())
	sdk.webserver.ConfigureStandardRoutes()

	sdk.LoggingClient.Info("Service started in: " + startupTimer.SinceAsString())

	return nil
}

// GetSecrets retrieves secrets from a secret store.
// path specifies the type or location of the secrets to retrieve. If specified it is appended
// to the base path from the SecretConfig
// keys specifies the secrets which to retrieve. If no keys are provided then all the keys associated with the
// specified path will be returned.
func (sdk *AppFunctionsSDK) GetSecrets(path string, keys ...string) (map[string]string, error) {
	return sdk.secretProvider.GetSecrets(path, keys...)
}

// StoreSecrets stores the secrets to a secret store.
// it sets the values requested at provided keys
// path specifies the type or location of the secrets to store. If specified it is appended
// to the base path from the SecretConfig
// secrets map specifies the "key": "value" pairs of secrets to store
func (sdk *AppFunctionsSDK) StoreSecrets(path string, secrets map[string]string) error {
	return sdk.secretProvider.StoreSecrets(path, secrets)
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
		sdk.serviceKeyOverride = envValue
		sdk.LoggingClient.Info(
			fmt.Sprintf("Environment profileOverride of '-n/--serviceName' by environment variable: %s=%s",
				envServiceKey,
				envValue))
	}

	// serviceKeyOverride may have been set by the -n/--serviceName command-line option and not the environment variable
	if len(sdk.serviceKeyOverride) > 0 {
		sdk.ServiceKey = sdk.serviceKeyOverride
	}

	if !strings.Contains(sdk.ServiceKey, ProfileSuffixPlaceholder) {
		// No placeholder, so nothing to do here
		return
	}

	// Have to handle environment override here before common bootstrap is used so it is passed the proper service key
	profileOverride := os.Getenv(envProfile)
	if len(profileOverride) > 0 {
		profile = profileOverride
	}

	if len(profile) > 0 {
		sdk.ServiceKey = strings.Replace(sdk.ServiceKey, ProfileSuffixPlaceholder, profile, 1)
		return
	}

	// No profile specified so remove the placeholder text
	sdk.ServiceKey = strings.Replace(sdk.ServiceKey, ProfileSuffixPlaceholder, "", 1)
}
