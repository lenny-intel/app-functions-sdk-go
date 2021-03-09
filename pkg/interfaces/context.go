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
	"time"

	"github.com/edgexfoundry/go-mod-core-contracts/v2/clients/command"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/clients/coredata"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/clients/logger"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/clients/notifications"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/v2/dtos"
)

// AppFunction is a type alias for func(appCxt AppFunctionContext, params ...interface{}) (bool, interface{})
type AppFunction = func(appCxt AppFunctionContext, data interface{}) (bool, interface{})

// AppFunctionContext define the interface for an Edgex Application Service Context used in the App Functions Pipeline
type AppFunctionContext interface {
	CorrelationID() string
	SetResponseData(output []byte)
	InputContentType() string
	SetResponseContentType(string)
	ResponseContentType() string
	SetRetryData(payload []byte)
	PushToCoreData(deviceName string, readingName string, value interface{}) (*dtos.Event, error)
	GetSecret(path string, keys ...string) (map[string]string, error)
	SecretsLastUpdated() time.Time
	LoggingClient() logger.LoggingClient
	EventClient() coredata.EventClient
	CommandClient() command.CommandClient
	NotificationsClient() notifications.NotificationsClient
}
