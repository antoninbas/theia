package sts

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/sts"
)

type Interface interface {
	GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}
