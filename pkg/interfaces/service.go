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

package interfaces

import (
	"net/http"

	"github.com/edgexfoundry/go-mod-core-contracts/v2/clients/command"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/clients/coredata"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/clients/logger"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/clients/notifications"
	"github.com/edgexfoundry/go-mod-registry/v2/registry"
)

const ProfileSuffixPlaceholder = "<profile>"

// ApplicationService defines the interface for an edgex Application Service
type ApplicationService interface {
	AddRoute(route string, handler func(http.ResponseWriter, *http.Request), methods ...string) error
	AddBackgroundPublisher(capacity int) BackgroundPublisher
	ApplicationSettings() map[string]string
	GetAppSettingStrings(setting string) ([]string, error)
	SetFunctionsPipeline(transforms ...AppFunction) error
	RegisterCustomTriggerFactory(name string, factory func(TriggerConfig) (Trigger, error)) error
	MakeItRun() error
	MakeItStop()
	LoadConfigurablePipeline() ([]AppFunction, error)
	GetSecret(path string, keys ...string) (map[string]string, error)
	StoreSecret(path string, secretData map[string]string) error
	LoggingClient() logger.LoggingClient
	EventClient() coredata.EventClient
	CommandClient() command.CommandClient
	NotificationsClient() notifications.NotificationsClient
	RegistryClient() registry.Client
}
