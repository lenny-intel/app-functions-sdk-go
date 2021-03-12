// TODO: Change Copyright to your company if open sourcing or remove header
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

package main

import (
	"os"

	"github.com/edgexfoundry/app-functions-sdk-go/v2/pkg"
	"github.com/edgexfoundry/app-functions-sdk-go/v2/pkg/transforms"

	"new-app-service/functions"
)

const (
	serviceKey = "new-app-service"
)

func main() {
	// TODO: See https://docs.edgexfoundry.org/1.3/microservices/application/ApplicationServices/
	//       for documentation on application services.

	service, ok := pkg.NewAppService(serviceKey)
	if !ok {
		os.Exit(-1)
	}

	lc := service.LoggingClient()

	// TODO: Replace with retrieving your custom ApplicationSettings from configuration
	deviceNames, err := service.GetAppSettingStrings("DeviceNames")
	if err != nil {
		lc.Errorf("failed to retrieve DeviceNames from configuration: %s", err.Error())
		os.Exit(-1)
	}

	// TODO: Replace below functions with built in and/or your custom functions for your use case.
	//       See https://docs.edgexfoundry.org/1.3/microservices/application/BuiltIn/ for list of built-in functions
	sample := functions.NewSample()
	err = service.SetFunctionsPipeline(
		transforms.NewFilterFor(deviceNames).FilterByDeviceName,
		sample.LogEventDetails,
		sample.ConvertEventToXML,
		sample.OutputXML)
	if err != nil {
		lc.Errorf("SetFunctionsPipeline returned error: %s", err.Error())
		os.Exit(-1)
	}

	if err := service.MakeItRun(); err != nil {
		lc.Errorf("MakeItRun returned error: %s", err.Error())
		os.Exit(-1)
	}

	// TODO: Do any required cleanup here, if needed

	os.Exit(0)
}
