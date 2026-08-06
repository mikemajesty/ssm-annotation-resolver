// Copyright 2026 SSM Annotation Resolver Authors
// SPDX-License-Identifier: Apache-2.0

package resolver

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

type ParameterGetter interface {
	GetParameter(ctx context.Context, parameterPath string) (string, error)
}

type awsParameterResolver struct {
	client         *ssm.Client
	withDecryption bool
}

func NewAWSParameterResolver(cfg aws.Config, withDecryption bool) ParameterGetter {
	return &awsParameterResolver{
		client:         ssm.NewFromConfig(cfg),
		withDecryption: withDecryption,
	}
}

func (r *awsParameterResolver) GetParameter(ctx context.Context, parameterPath string) (string, error) {
	out, err := r.client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(parameterPath),
		WithDecryption: aws.Bool(r.withDecryption),
	})
	if err != nil {
		return "", fmt.Errorf("fetching parameter %q from ssm: %w", parameterPath, err)
	}

	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", errors.New("ssm parameter response did not include a value")
	}

	return *out.Parameter.Value, nil
}

func LoadAWSConfig(ctx context.Context, region string) (aws.Config, error) {
	loadOptions := make([]func(*awscfg.LoadOptions) error, 0, 1)
	if region != "" {
		loadOptions = append(loadOptions, awscfg.WithRegion(region))
	}

	cfg, err := awscfg.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return aws.Config{}, err
	}

	return cfg, nil
}
