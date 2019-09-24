# **Seoul F2F Training Exercise** 

**This exercise will expand the previous demo defined in [ReadMe.md](https://github.com/lenny-intel/app-functions-sdk-go/blob/master/examples/advanced-filter-convert-publish/README.md) by adding the following:**

1. Also filter for RandomValue_Int32
2. Use the random Int32 as the number of decimal places for the human readable floats.
3. Add sending a command to generate the random Int32 after every 4 floats received.  

### **Modify Device Virtual config and device** **yml** **as follows:**

1. Stop **device-virtual** service

2. In cmd/res/configuration.toml remove Auto Event for **RandomValue_Int32** on **Random-Integer-Generator01**

   - Only generate random Int32 when commanded

3. In cmd/res/device.virtual.float.yaml under **deviceResources** add minimum and maximum values for RandomValue_Int32 as follow:

    ```{ type: "Int32", readWrite: "R", defaultValue: "0“, minimum: "1", maximum: "6" }```

   - Restricts the decimal places  to 1-6 

4. In cmd/res/device.virtual.float.yaml under **deviceResources** add minimum and maximum values for RandomValue_Float32 & RandomValue_Float64 as follow:

    ```{ type: "Float32", readWrite: "R", defaultValue: "0" , minimum: "1.0", maximum: "1.9"}```

    ```{ type: "Float64", readWrite: "R", defaultValue: "0" , minimum: "2.0", maximum: "2.9"}```

   - Keeps the random floats to small values.

5. Run the following curl commands before starting device-virtual to remove old profiles and device configuration:

   - curl -X DELETE http://localhost:48081/api/v1/deviceservice/name/device-virtual
   - curl -X DELETE http://localhost:48081/api/v1/deviceprofile/name/Random-Integer-Generator
   - curl -X DELETE http://localhost:48081/api/v1/deviceprofile/name/Random-Float-Generator

6. Now run device-virtual

   - Run **./device-virtual** from cmd folder 

### **Modify advanced-filter-convert-publish (Set Precision)**

These changes filter for RandomValue_Float32, RandomValue_Float64, RandomValue_Int32 values, then set the float conversion precision to the Int32 value when received. Remember that we turned off auto events for Int32, so now we have to manually trigger them with a command. 

1. cd to examples/advanced-filter-convert-publish

2. In res/configuration.toml **ApplicationSetting** section, add  **RandomValue_Int32** to **ValueDescriptors**: 

   ```toml
   [ApplicationSettings]
    ApplicationName = "advanced-filter-convert-publish“
    ValueDescriptors = "RandomValue_Float32, RandomValue_Float64, RandomValue_Int32"
   ```

3. Create precision.go in functions folder with the following contents

   ```go
   package functions
   
   import (
   	"fmt"
   	"strconv"
   
   	"github.com/edgexfoundry/app-functions-sdk-go/appcontext"
   	"github.com/edgexfoundry/go-mod-core-contracts/models"
   )
   
   func SetPrecision(edgexcontext *appcontext.Context, params ...interface{}) (bool, interface{}) {
   	edgexcontext.LoggingClient.Debug("Set precision for float conversion to Int32 reading value")
   
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
   
   			return false, nil  // Terminates the functions pipeline execution
   		}
   	}
   
   	return true, event  // Continues the functions pipeline execution with the current event
   }
   ```

4. Modify main.go as follows

   Add SetPrecision function to the Functions Pipeline as following:

   ```go
   	// 4) This is our functions pipeline configuration, the collection of functions to
   	// execute every time an event is triggered.
   	err := edgexSdk.SetFunctionsPipeline(
   		edgexSdk.ValueDescriptorFilter(valueDescriptors),
   		functions.SetPrecision, // <--- Insert function
   		functions.ConvertToReadableFloatValues,
   		functions.PrintFloatValuesToConsole,
   		functions.Publish,
   	)
   ```

5. Build and run advanced-filter-convert-publish

   - Run **make build**
   - Run **./advanced-filter-convert-publish**

6. Manually generate Int32 values

   - Run **curl http://localhost:48082/api/v1/device/name/Random-Integer-Generator01/command/RandomValue_Int32** multiple times.

By default the precision starts out at 4 decimal points. Then it changes each time we run the above curl command. Below is what the output will look like. 

```reStructuredText
RandomValue_Float32 readable value from Random-Float-Generator01 is 1.1356

RandomValue_Float64 readable value from Random-Float-Generator01 is 2.0135

level=INFO ts=2019-04-25T20:22:34.725700483Z app=AppFunctionsSDK source=precision.go:27 msg="Float conversion precision has been set to 3"

RandomValue_Float32 readable value from Random-Float-Generator01 is 1.105

RandomValue_Float64 readable value from Random-Float-Generator01 is 2.084

RandomValue_Float32 readable value from Random-Float-Generator01 is 1.838

RandomValue_Float64 readable value from Random-Float-Generator01 is 2.154

level=INFO ts=2019-04-25T20:22:49.790309819Z app=AppFunctionsSDK source=precision.go:27 msg="Float conversion precision has been set to 6"

RandomValue_Float32 readable value from Random-Float-Generator01 is 1.334197

RandomValue_Float64 readable value from Random-Float-Generator01 is 2.319008
```



### **Modify advanced-filter-convert-publish (Send Command)**

Now lets add sending the command automatically when a certain condition has been meet, say every 4th float received.

1. Add the **Int32Command** setting to ApplicationSettings in re/configuration.toml

   ```toml
   [ApplicationSettings]
   ApplicationName = "advanced-filter-convert-publish"
   ValueDescriptors = "RandomValue_Float32, RandomValue_Float64, RandomValue_Int32"
   Int32Command = “http://localhost:48082/api/v1/device/name/Random-Integer-Generator01/command/RandomValue_Int32”
   ```

2. Create command.go in functions folder with the following contents

   ```go
   package functions
   
   import (
   	"fmt"
   	"net/http"
   
   	"github.com/edgexfoundry/app-functions-sdk-go/appcontext"
   	"github.com/edgexfoundry/go-mod-core-contracts/models"
   )
   
   const targetFloatCount = 4
   
   var floatCountDown = targetFloatCount
   
   func SendInt32Command(edgexcontext *appcontext.Context, params ...interface{}) (bool, interface{}) {
   	edgexcontext.LoggingClient.Debug("Send the Int32 command every forth float received")
   
   	if len(params) < 1 {
   		// We didn't receive a result
   		return false, nil
   	}
   
   	event := params[0].(models.Event)
   
   	if event.Device == "Random-Float-Generator01" {
   		floatCountDown--
   		if floatCountDown == 0 {
   			floatCountDown = targetFloatCount
   			sendCommand(edgexcontext)
   		}
   	}
   
   	return true, event  // Continues the functions pipeline execution with the current event
   }
   
   func sendCommand(edgexcontext *appcontext.Context) {
   	commandUrl, ok := edgexcontext.Configuration.ApplicationSettings["Int32Command"]
   	if !ok {
   		edgexcontext.LoggingClient.Error("Int32Command Application setting not found")
   		return
   	}
   
   
   	resp, err := http.Get(commandUrl)
   	if err != nil {
   		edgexcontext.LoggingClient.Error(fmt.Sprintf("Error sending Int32Command: %s", err.Error()))
   		return
   	}
   
   	if resp.StatusCode != http.StatusOK {
   		edgexcontext.LoggingClient.Error(fmt.Sprintf("Error sending Int32Command: Received status code %s",resp.StatusCode))
   		return
   	}
   }
   ```

   

3. Add **SendInt32Command** to the functions pipeline in main.go

   ```go
   	// execute every time an event is triggered.
   	err := edgexSdk.SetFunctionsPipeline(
   		edgexSdk.ValueDescriptorFilter(valueDescriptors),
   		functions.SendInt32Command, // <--- Insert function
   		functions.SetPrecision,
   		functions.ConvertToReadableFloatValues,
   		functions.PrintFloatValuesToConsole,
   		functions.Publish,
   	)
   ```

4. Build and run advanced-filter-convert-publish

   - Run **make build**
   - Run **./advanced-filter-convert-publish**

Now the Int32 command is generated automatically every forth float received. Output will look like this:

```reStructuredText
RandomValue_Float32 readable value from Random-Float-Generator01 is 1.0893

RandomValue_Float64 readable value from Random-Float-Generator01 is 2.4203

RandomValue_Float32 readable value from Random-Float-Generator01 is 1.0018

RandomValue_Float64 readable value from Random-Float-Generator01 is 2.7821

level=INFO ts=2019-04-25T22:07:39.475470902Z app=AppFunctionsSDK source=precision.go:28 msg="Float conversion precision has been set to 2"

RandomValue_Float32 readable value from Random-Float-Generator01 is 1.25

RandomValue_Float64 readable value from Random-Float-Generator01 is 2.16

RandomValue_Float32 readable value from Random-Float-Generator01 is 1.05

RandomValue_Float64 readable value from Random-Float-Generator01 is 2.13

level=INFO ts=2019-04-25T22:07:49.709453946Z app=AppFunctionsSDK source=precision.go:28 msg="Float conversion precision has been set to 6"

RandomValue_Float32 readable value from Random-Float-Generator01 is 1.790991

RandomValue_Float64 readable value from Random-Float-Generator01 is 2.486688
```