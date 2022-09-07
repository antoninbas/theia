package iam

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
)

func GetClient(cfg aws.Config) Interface {
	return iam.NewFromConfig(cfg)
}
