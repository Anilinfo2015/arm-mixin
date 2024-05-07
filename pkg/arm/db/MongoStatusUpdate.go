package db

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"gopkg.in/mgo.v2/bson"
)

type MongoConfiguration struct {
	MongoClient    *mongo.Client
	DatabaseName   string
	CollectionName string
}

// Package represents a document in the collection
type Status struct {
	Id                    bson.ObjectId `bson:"_id,omitempty"`
	SubscriptionId        string
	ResourceGroupName     string
	ResourceName          string
	ItemName              string
	ItemType              string
	MixInName             string
	IsActive              bool
	ExecutionStatus       string
	StatusReportedOn      time.Time
	InstallationName      string
	InstallationNameSpace string
	CorrelationId         string
	PorterCorrelationId   string
	CnabRevision          string
	Output                string
}

type StatusRepository struct {
	StatusCollection *mongo.Collection
}

// // NewStatusRepository creates a new instance of StatusRepository
func NewStatusRepository(configuration MongoConfiguration) *StatusRepository {

	var mongoCollection = configuration.MongoClient.Database(configuration.DatabaseName).Collection(configuration.CollectionName)

	return &StatusRepository{StatusCollection: mongoCollection}
}

// // RecordStatus records the status of a package
func (statusRepository *StatusRepository) RecordStatus(status Status) (*mongo.UpdateResult, error) {

	filter := bson.M{}

	filter["subscriptionid"] = status.SubscriptionId
	filter["resourcegroupname"] = status.ResourceGroupName
	filter["CorrelationId"] = status.CorrelationId
	filter["mixinname"] = status.MixInName
	filter["isactive"] = true

	result, err := statusRepository.StatusCollection.ReplaceOne(context.Background(), filter, status, options.Replace().SetUpsert(true))

	return result, err
}

// Get status returns the status for the given subscriptionId, resourceGroupName and resourceName
func (statusRepository *StatusRepository) GetStatus(subscriptionId string, resourceGroupName string, resourceName string) ([]Status, error) {

	filter := bson.M{}

	filter["subscriptionid"] = subscriptionId
	filter["resourcegroupname"] = resourceGroupName
	filter["resourcename"] = resourceName
	filter["isactive"] = true

	cursor, err := statusRepository.StatusCollection.Find(context.Background(), filter)

	if err != nil {
		return nil, err
	}

	var results []Status

	if err = cursor.All(context.Background(), &results); err != nil {
		return nil, err
	}

	return results, nil
}

// /helper helps in creating mongo client and provide function to disconnect
type MongoClientHelper struct {
	MongoClient *mongo.Client
}

// / InitializeMongoClient initializes a connection to the MongoDB server
func NewMongoClientHelper(connectionString string) (*MongoClientHelper, error) {

	//The context.Background() is the root context and WithTimeout is a function
	// that creates a new context that carries a deadline.
	//The cancel function is used to release resources if the operation completes before the timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)

	//n the context of your code, defer cancel() will ensure that the cancel function you obtained from
	// context.WithTimeout is called when the current function exits, even if it exits because an error
	//occurred while connecting to MongoDB.
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(connectionString))

	if err != nil {
		return nil, err
	}

	//Calling Connect does not block for server discovery. If you wish to know if a MongoDB server has been found and connected to, use the Ping method:
	err = client.Ping(ctx, readpref.Primary())

	if err != nil {
		return nil, err
	}

	return &MongoClientHelper{MongoClient: client}, nil

}

// / DisconnectMongoClient disconnects the client from the MongoDB server
func (mongoClientHelper *MongoClientHelper) DisconnectMongoClient() error {

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)

	defer cancel()

	err := mongoClientHelper.MongoClient.Disconnect(ctx)

	if err != nil {
		return err
	}

	return nil
}
