package sns

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
)

func GetClient(cfg aws.Config) Interface {
	return sns.NewFromConfig(cfg)
}
