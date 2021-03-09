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

package appcontext

import (
	"context"
	"fmt"
	"time"

	bootstrapContainer "github.com/edgexfoundry/go-mod-bootstrap/v2/bootstrap/container"
	"github.com/edgexfoundry/go-mod-bootstrap/v2/di"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/clients/command"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/clients/coredata"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/clients/logger"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/clients/notifications"

	"github.com/edgexfoundry/app-functions-sdk-go/v2/internal/bootstrap/container"
	"github.com/edgexfoundry/app-functions-sdk-go/v2/internal/bootstrap/handlers"
	"github.com/edgexfoundry/app-functions-sdk-go/v2/pkg/util"

	"github.com/edgexfoundry/go-mod-core-contracts/v2/clients"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/models"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/v2"
	"github.com/edgexfoundry/go-mod-core-contracts/v2/v2/dtos"
	commonDTO "github.com/edgexfoundry/go-mod-core-contracts/v2/v2/dtos/common"

	"github.com/google/uuid"
)

func New(correlationID string, dic *di.Container, inputContentType string) *Context {
	return &Context{
		correlationID:    correlationID,
		dic:              dic,
		inputContentType: inputContentType,
	}
}

// Context contains the data and dependencies as the context for the Application Functions
type Context struct {
	dic                 *di.Container
	correlationID       string
	inputContentType    string
	responseData        []byte
	retryData           []byte
	responseContentType string
}

// SetCorrelationID sets the correlationID. This function is not part of the AppFunctionContext interface,
// so it is internal SDK use only
func (appContext *Context) SetCorrelationID(id string) {
	appContext.correlationID = id
}

func (appContext *Context) CorrelationID() string {
	return appContext.correlationID
}

// SetInputContentType sets the inputContentType. This function is not part of the AppFunctionContext interface,
// so it is internal SDK use only
func (appContext *Context) SetInputContentType(contentType string) {
	appContext.inputContentType = contentType
}

func (appContext *Context) InputContentType() string {
	return appContext.inputContentType
}

// SetResponseData provides a way to return the specified data as a response to the trigger that initiated
// the execution of the function pipeline. In the case of an HTTP Trigger, the data will be returned as the http response.
// In the case of a message bus trigger, the data will be published to the configured message bus publish topic.
func (appContext *Context) SetResponseData(output []byte) {
	appContext.responseData = output
}

// ResponseData gets the responseData. This function is not part of the AppFunctionContext interface,
// so it is internal SDK use only

func (appContext *Context) ResponseData() []byte {
	return appContext.responseData
}

func (appContext *Context) SetResponseContentType(contentType string) {
	appContext.responseContentType = contentType
}

func (appContext *Context) ResponseContentType() string {
	return appContext.responseContentType
}

// SetRetryData sets the retryData to the specified payload to be stored for later retry
// when the pipeline function returns an error.
func (appContext *Context) SetRetryData(payload []byte) {
	appContext.retryData = payload
}

// RetryData gets the retryData. This function is not part of the AppFunctionContext interface,
// so it is internal SDK use only
func (appContext *Context) RetryData() []byte {
	return appContext.retryData
}

// PushToCoreData pushes the provided value as an event to CoreData using the device name and reading name that have been set. If validation is turned on in
// CoreServices then your deviceName and readingName must exist in the CoreMetadata and be properly registered in EdgeX.
func (appContext *Context) PushToCoreData(deviceName string, readingName string, value interface{}) (*dtos.Event, error) {
	lc := appContext.LoggingClient()
	lc.Debug("Pushing to CoreData")

	if appContext.EventClient() == nil {
		return nil, fmt.Errorf("unable to Push To CoreData: '%s' is missing from Clients configuration", handlers.CoreDataClientName)
	}

	now := time.Now().UnixNano()
	val, err := util.CoerceType(value)
	if err != nil {
		return nil, err
	}

	// Temporary use V1 Reading until V2 EventClient is available
	// TODO: Change to use dtos.Reading
	v1Reading := models.Reading{
		Value:     string(val),
		ValueType: v2.ValueTypeString,
		Origin:    now,
		Device:    deviceName,
		Name:      readingName,
	}

	readings := make([]models.Reading, 0, 1)
	readings = append(readings, v1Reading)

	// Temporary use V1 Event until V2 EventClient is available
	// TODO: Change to use dtos.Event
	v1Event := &models.Event{
		Device:   deviceName,
		Origin:   now,
		Readings: readings,
	}

	correlation := uuid.New().String()
	ctx := context.WithValue(context.Background(), clients.CorrelationHeader, correlation)
	result, err := appContext.EventClient().Add(ctx, v1Event) // TODO: Update to use V2 EventClient
	if err != nil {
		return nil, err
	}
	v1Event.ID = result

	// TODO: Remove once V2 EventClient is available
	v2Reading := dtos.BaseReading{
		Versionable:   commonDTO.NewVersionable(),
		Id:            v1Reading.Id,
		Created:       v1Reading.Created,
		Origin:        v1Reading.Origin,
		DeviceName:    v1Reading.Device,
		ResourceName:  v1Reading.Name,
		ProfileName:   "",
		ValueType:     v1Reading.ValueType,
		SimpleReading: dtos.SimpleReading{Value: v1Reading.Value},
	}

	// TODO: Remove once V2 EventClient is available
	v2Event := dtos.Event{
		Versionable: commonDTO.NewVersionable(),
		Id:          result,
		DeviceName:  v1Event.Device,
		Origin:      v1Event.Origin,
		Readings:    []dtos.BaseReading{v2Reading},
	}
	return &v2Event, nil
}

func (appContext *Context) GetSecret(path string, keys ...string) (map[string]string, error) {
	secretProvider := bootstrapContainer.SecretProviderFrom(appContext.dic.Get)
	return secretProvider.GetSecrets(path, keys...)
}

func (appContext *Context) SecretsLastUpdated() time.Time {
	secretProvider := bootstrapContainer.SecretProviderFrom(appContext.dic.Get)
	return secretProvider.SecretsLastUpdated()
}

func (appContext *Context) LoggingClient() logger.LoggingClient {
	return bootstrapContainer.LoggingClientFrom(appContext.dic.Get)
}

func (appContext *Context) EventClient() coredata.EventClient {
	return container.EventClientFrom(appContext.dic.Get)
}

func (appContext *Context) CommandClient() command.CommandClient {
	return container.CommandClientFrom(appContext.dic.Get)
}

func (appContext *Context) NotificationsClient() notifications.NotificationsClient {
	return container.NotificationsClientFrom(appContext.dic.Get)

}
