package iam

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/iam"
)

type Interface interface {
	CreatePolicy(ctx context.Context, params *iam.CreatePolicyInput, optFns ...func(*iam.Options)) (*iam.CreatePolicyOutput, error)
	GetPolicy(ctx context.Context, params *iam.GetPolicyInput, optFns ...func(*iam.Options)) (*iam.GetPolicyOutput, error)

	CreateRole(ctx context.Context, params *iam.CreateRoleInput, optFns ...func(*iam.Options)) (*iam.CreateRoleOutput, error)
	GetRole(ctx context.Context, params *iam.GetRoleInput, optFns ...func(*iam.Options)) (*iam.GetRoleOutput, error)

	AttachRolePolicy(ctx context.Context, params *iam.AttachRolePolicyInput, optFns ...func(*iam.Options)) (*iam.AttachRolePolicyOutput, error)

	UpdateAssumeRolePolicy(ctx context.Context, params *iam.UpdateAssumeRolePolicyInput, optFns ...func(*iam.Options)) (*iam.UpdateAssumeRolePolicyOutput, error)
}
