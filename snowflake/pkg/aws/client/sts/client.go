package sts

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

func GetClient(cfg aws.Config) Interface {
	return sts.NewFromConfig(cfg)
}
