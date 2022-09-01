package onboard

import (
	"bytes"
	"fmt"
	"text/template"
)

const snowflakeStoragePolicyDocument = `{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Effect": "Allow",
            "Action": [
              "s3:GetObject",
              "s3:GetObjectVersion"
            ],
            "Resource": "arn:aws:s3:::{{.Bucket}}/{{.Prefix}}/*"
        },
        {
            "Effect": "Allow",
            "Action": [
                "s3:ListBucket"
            ],
            "Resource": "arn:aws:s3:::{{.Bucket}}",
            "Condition": {
                "StringLike": {
                    "s3:prefix": [
                        "{{.Prefix}}/*"
                    ]
                }
            }
        },
        {
            "Effect": "Allow",
            "Action": [
                "s3:GetBucketLocation"
            ],
            "Resource": "arn:aws:s3:::{{.Bucket}}"
        }
    ]
}`

var snowflakeStoragePolicyDocumentTemplate = template.Must(template.New("snowflakeStoragePolicyDocument").Parse(snowflakeStoragePolicyDocument))

const snowflakeAssumeRolePolicyDocument = `{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Effect": "Allow",
            "Action": "sts:AssumeRole",
            "Principal": {
                "AWS": "{{.AccountID}}"
            },
            "Condition": {
                "StringEquals": {
                    "sts:ExternalId": "{{.ExternalID}}"
                }
            }
        }
    ]
}`

var snowflakeAssumeRolePolicyDocumentTemplate = template.Must(template.New("snowflakeAssumeRolePolicyDocument").Parse(snowflakeAssumeRolePolicyDocument))

const snowflakeNotificationPolicyDocument = `{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Effect": "Allow",
            "Action": [
              "sns:Publish"
            ],
            "Resource": "{{.SNSTopicARN}}"
        }
    ]
}`

var snowflakeNotificationPolicyDocumentTemplate = template.Must(template.New("snowflakeNotificationPolicyDocument").Parse(snowflakeNotificationPolicyDocument))

func getSnowflakeStoragePolicyDocument(bucket string, prefix string) (string, error) {
	type PolicyDocumentParams struct {
		Bucket string
		Prefix string
	}

	var policyDocument bytes.Buffer
	if err := snowflakeStoragePolicyDocumentTemplate.Execute(&policyDocument, PolicyDocumentParams{
		Bucket: bucket,
		Prefix: prefix,
	}); err != nil {
		return "", fmt.Errorf("error when executing template for policy document: %w", err)
	}

	return policyDocument.String(), nil
}

func getSnowflakeNotificationPolicyDocument(snsTopicARN string) (string, error) {
	type PolicyDocumentParams struct {
		SNSTopicARN string
	}

	var policyDocument bytes.Buffer
	if err := snowflakeNotificationPolicyDocumentTemplate.Execute(&policyDocument, PolicyDocumentParams{
		SNSTopicARN: snsTopicARN,
	}); err != nil {
		return "", fmt.Errorf("error when executing template for policy document: %w", err)
	}

	return policyDocument.String(), nil
}

func getSnowflakeAssumeRolePolicyDocument(accountID string, externalID string) (string, error) {
	type AssumeRolePolicyDocumentParams struct {
		AccountID  string
		ExternalID string
	}

	var assumeRolePolicyDocument bytes.Buffer
	if err := snowflakeAssumeRolePolicyDocumentTemplate.Execute(&assumeRolePolicyDocument, AssumeRolePolicyDocumentParams{
		AccountID:  accountID,
		ExternalID: externalID,
	}); err != nil {
		return "", fmt.Errorf("error when executing template for assumeRolePolicy document: %w", err)
	}

	return assumeRolePolicyDocument.String(), nil
}
