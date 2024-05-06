package arm

import (
	"context"
	"fmt"
	"strings"

	"encoding/json"

	"time"

	"get.porter.sh/mixin/arm/pkg/arm/db"
	"github.com/pkg/errors"
	yaml "gopkg.in/yaml.v2"
)

type InstallAction struct {
	Steps []InstallStep `yaml:"install"`
}

type InstallStep struct {
	InstallArguments `yaml:"arm"`
}

type InstallArguments struct {
	Step `yaml:",inline"`

	Template      string                 `yaml:"template"`
	Name          string                 `yaml:"name"`
	ResourceGroup string                 `yaml:"resourceGroup"`
	Parameters    map[string]interface{} `yaml:"parameters"`
	Settings      map[string]interface{} `yaml:"settings"`
}

func parseInstallAction(payload []byte) (InstallArguments, error) {
	var action InstallAction
	err := yaml.Unmarshal(payload, &action)
	if err != nil {
		return InstallArguments{}, err
	}
	if len(action.Steps) != 1 {
		return InstallArguments{}, errors.Errorf("expected a single step, but got %d", len(action.Steps))
	}
	step := action.Steps[0]
	return step.InstallArguments, nil
}

/*
Install the ARM template
--------------------------
1. Get payload from install action step
2. Get the template from the arguments
3. Get the resource group from the arguments
4. Get the parameters from the arguments
5. Get the settings from the arguments
6. Get the polling duration from the settings
7. Get the correlation id from the parameters
8. Create a new mongo repository if Microsoft_StatusDBConnectionString is not empty
9. Update the status to "Succeeded" in the database
10. Return nil on success
*/
func (m *Mixin) Install(ctx context.Context) error {
	payload, err := m.getPayloadData()
	if err != nil {
		return err
	}

	installArguments, err := parseInstallAction(payload)
	if err != nil {
		return err
	}
	err = validateInstallArguments(installArguments)
	if err != nil {
		return err
	}
	pollingDuration := getPollingDuration(installArguments)
	var correlationId string = ""
	correlationId = getCorrelationId(installArguments, m, correlationId)

	// Get the arm deployer
	deployer, err := m.getARMDeployer(pollingDuration)
	if err != nil {
		return err
	}
	// Get the Template based on the arguments (type)
	template, err := deployer.FindTemplate(installArguments.Template)
	if err != nil {
		return err
	}
	azureConfig := m.cfg
	mongoClientHelper, repository, err := createMongoRepository(azureConfig.Microsoft_StatusDBConnectionString, getDatabaseName(installArguments), getCollectionName(installArguments))
	if err != nil {
		fmt.Fprintf(m.Out, "[correlationId : %s] Microsoft_StatusDBConnectionString is empty/invalid\n", correlationId)
	}

	fmt.Fprintf(m.Out, "[correlationId: %s] Starting deployment operations...\n", correlationId)
	fmt.Fprintf(m.Out, "[correlationId: %s] Template location %s...\n", correlationId, installArguments.Template)
	// call Deployer.Deploy(...)
	outputs, err := deployer.Deploy(
		installArguments.Name,
		installArguments.ResourceGroup,
		installArguments.Parameters["location"].(string),
		template,
		installArguments.Parameters, // arm params
	)
	if err != nil {
		updateStatus(repository, m, "Failed", installArguments, correlationId, err.Error(), azureConfig.SubscriptionID)
		return err
	}
	fmt.Fprintf(m.Out, "[correlationId: %s] Finished deployment operations...\n", correlationId)

	// ARM does some stupid stuff with output keys, turn them
	// all into upper case for better matching
	// ToUpper the key because of the case weirdness with ARM outputs
	outputStr := processArmOutput(outputs, installArguments, m, correlationId)

	updateStatus(repository, m, "Succeeded", installArguments, correlationId, outputStr, azureConfig.SubscriptionID)
	if repository != nil {
		mongoClientHelper.DisconnectMongoClient()
	}
	return nil
}

// getCorrelationId gets the correlation id from the parameters
func getCorrelationId(installArguments InstallArguments, m *Mixin, correlationId string) string {
	if _, ok := installArguments.Parameters["correlationId"]; !ok {
		fmt.Fprintln(m.Out, "correlationId is missing in parameters.")
	} else {
		correlationId = installArguments.Parameters["correlationId"].(string)
		delete(installArguments.Parameters, "correlationId")
	}
	return correlationId
}

// validateInstallArguments validates the install arguments
func validateInstallArguments(installArguments InstallArguments) error {
	if installArguments.Template == "" {
		return errors.New("template is required")
	}
	if installArguments.Name == "" {
		return errors.New("name is required")
	}
	if installArguments.ResourceGroup == "" {
		return errors.New("resourceGroup is required")
	}
	if installArguments.Parameters == nil {
		return errors.New("parameters is required")
	}
	if installArguments.Parameters["location"] == nil {
		return errors.New("location is required in parameters")
	}
	if _, ok := installArguments.Parameters["location"].(string); !ok {
		return errors.New("location must be a string")
	}
	return nil
}

// processArmOutput processes the ARM outputs
func processArmOutput(outputs map[string]interface{}, installArguments InstallArguments, m *Mixin, correlationId string) string {
	for k, v := range outputs {
		newKey := strings.ToUpper(k)
		outputs[newKey] = v
	}
	outputMap := make(map[string]interface{})

	for _, output := range installArguments.Outputs {
		v, ok := outputs[strings.ToUpper(output.Key)]
		if !ok {
			return ""
		}
		outputMap[output.Key] = v
	}
	jsonString, err := json.Marshal(outputMap)

	fmt.Fprintf(m.Out, "[correlationId : %s] Output : %s\n", correlationId, jsonString)

	if err != nil {
		return ""
	}

	// Write the JSON string to a file
	err = m.WriteMixinOutputToFile("output.json", jsonString)
	if err != nil {
		return string(jsonString)
	}

	return string(jsonString)
}

// getPollingDuration gets the polling duration from the settings
func getPollingDuration(installArguments InstallArguments) int {
	var pollingDuration int = 30
	settings := installArguments.Settings
	if settings != nil {

		if duration, ok := settings["pollingDuration"].(int); ok {
			pollingDuration = duration
		}
	}
	return pollingDuration
}

// getDatabaseName gets the database name from the settings
func getDatabaseName(installArguments InstallArguments) string {
	var databaseName string = "porter"
	settings := installArguments.Settings
	if settings != nil {

		if dbName, ok := settings["databaseName"].(string); ok {
			databaseName = dbName
		}
	}
	return databaseName
}

// getCollectionName gets the collection name from the settings
func getCollectionName(installArguments InstallArguments) string {
	var collectionName string = "status"
	settings := installArguments.Settings
	if settings != nil {

		if cname, ok := settings["collectionName"].(string); ok {
			collectionName = cname
		}
	}
	return collectionName
}

// createMongoRepository creates a new mongo repository
func createMongoRepository(connectionStr string, databaseName string, collectionName string) (*db.MongoClientHelper, *db.StatusRepository, error) {
	if connectionStr == "" {
		return nil, nil, errors.New("connection string is required")
	}
	mongoClientHelper, err := db.NewMongoClientHelper(connectionStr)
	if err != nil {
		return nil, nil, err
	}

	mongoClient := mongoClientHelper.MongoClient

	configuration := db.MongoConfiguration{
		MongoClient:    mongoClient,
		DatabaseName:   databaseName,
		CollectionName: collectionName,
	}

	return mongoClientHelper, db.NewStatusRepository(configuration), nil
}

// updateStatus updates the status of the installation in the database
func updateStatus(repository *db.StatusRepository, m *Mixin, statusValue string, installArguments InstallArguments, correlationId string, output string, subscriptionId string) {
	if repository == nil {
		return
	}
	status := db.Status{
		SubscriptionId:      subscriptionId,
		ResourceGroupName:   installArguments.ResourceGroup,
		ItemName:            "arm template",
		ItemType:            "arm",
		InstallationName:    installArguments.Template,
		MixInName:           "arm",
		IsActive:            true,
		ExecutionStatus:     statusValue,
		StatusReportedOn:    time.Now(),
		CorrelationId:       correlationId,
		PorterCorrelationId: correlationId,
		Output:              output,
	}
	_, err := repository.RecordStatus(status)
	if err != nil {
		fmt.Fprintf(m.Out, "[correlationId : %s] Error while updating status\n", correlationId)
	}
}
