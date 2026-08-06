// Copyright 2026 SSM Annotation Resolver Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	goruntime "runtime"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	ctrlwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/mikemajesty/ssm-annotation-resolver/api/v1"
	"github.com/mikemajesty/ssm-annotation-resolver/pkg"
	"github.com/mikemajesty/ssm-annotation-resolver/pkg/reconciler"
	"github.com/mikemajesty/ssm-annotation-resolver/pkg/resolver"
)

func main() {
	// Setting up logger
	ctrl.SetLogger(zap.New())

	setupLog := ctrl.Log.WithName("setup")
	// CLI arguments
	var (
		metricsBindAddress     string
		healthProbeBindAddress string
		leaderElect            bool
		managerNamespace       string
		webhookPort            int
		webhookCertDir         string
		awsRegion              string
		ssmWithDecryption      bool
		sqsQueueURL            string
		sqsWaitTimeSeconds     int
		sqsMaxMessages         int
	)

	zapfs := flag.NewFlagSet("zap", flag.ExitOnError)
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(zapfs)
	zapfs.StringVar(&metricsBindAddress, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	zapfs.StringVar(&healthProbeBindAddress, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	zapfs.BoolVar(&leaderElect, "leader-elect", false, "Enable leader election for controller manager.")
	zapfs.StringVar(&managerNamespace, "manager-namespace", os.Getenv("POD_NAMESPACE"), "The namespace of the manager instance.")
	zapfs.IntVar(&webhookPort, "webhook-port", 9443, "The secure webhook server port.")
	zapfs.StringVar(&webhookCertDir, "webhook-cert-dir", "/tmp/k8s-webhook-server/serving-certs", "Directory containing tls.crt and tls.key for webhook TLS.")
	zapfs.StringVar(&awsRegion, "aws-region", os.Getenv("AWS_REGION"), "AWS region used by SSM/SQS clients.")
	zapfs.BoolVar(&ssmWithDecryption, "ssm-with-decryption", true, "Whether SSM GetParameter should decrypt SecureString values.")
	zapfs.StringVar(&sqsQueueURL, "sqs-queue-url", os.Getenv("SQS_QUEUE_URL"), "SQS queue URL used for SSM Parameter Store change events.")
	zapfs.IntVar(&sqsWaitTimeSeconds, "sqs-wait-time-seconds", 20, "SQS long-poll wait time in seconds.")
	zapfs.IntVar(&sqsMaxMessages, "sqs-max-messages", 10, "Maximum messages to fetch per SQS poll.")

	if err := zapfs.Parse(os.Args[1:]); err != nil {
		setupLog.Error(err, "Failed to parse flags")
		os.Exit(1)
	}

	gitRepo, gitSha, buildTime := pkg.BuildInfo()

	setupLog.Info(fmt.Sprintf("Build version: %s %s", pkg.GitTag, gitSha))
	setupLog.Info(fmt.Sprintf("Build from: %s", gitRepo))
	setupLog.Info(fmt.Sprintf("Build date: %s", buildTime))
	setupLog.Info(fmt.Sprintf("Go Version: %s", goruntime.Version()))
	setupLog.Info(fmt.Sprintf("Go OS/Arch: %s/%s", goruntime.GOOS, goruntime.GOARCH))

	scheme := runtime.NewScheme()

	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		setupLog.Error(err, "unable to add client to scheme")
		os.Exit(1)
	}

	// Register SSM Annotation Resolver CRD
	if err := v1.AddToScheme(scheme); err != nil {
		setupLog.Error(err, "unable to add SSM Annotation Resolver types to scheme")
		os.Exit(1)
	}

	ctrlOpts := ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsBindAddress,
		},
		HealthProbeBindAddress:  healthProbeBindAddress,
		LeaderElection:          leaderElect,
		LeaderElectionNamespace: managerNamespace,
		LeaderElectionID:        "ssm-annotation-resolver.io",
		WebhookServer: ctrlwebhook.NewServer(ctrlwebhook.Options{
			Port:    webhookPort,
			CertDir: webhookCertDir,
		}),
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrlOpts)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	awsConfig, err := resolver.LoadAWSConfig(context.Background(), awsRegion)
	if err != nil {
		setupLog.Error(err, "unable to load AWS SDK configuration")
		os.Exit(1)
	}

	parameterResolver := resolver.NewAWSParameterResolver(awsConfig, ssmWithDecryption)
	mgr.GetWebhookServer().Register(
		"/mutate",
		&admission.Webhook{
			Handler: resolver.NewWebhookHandler(parameterResolver, ctrl.Log.WithName("webhook")),
		},
	)

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(mgr.GetConfig())
	if err != nil {
		setupLog.Error(err, "unable to create discovery client")
		os.Exit(1)
	}

	dynamicClient, err := dynamic.NewForConfig(mgr.GetConfig())
	if err != nil {
		setupLog.Error(err, "unable to create dynamic client")
		os.Exit(1)
	}

	if err = mgr.Add(reconciler.NewRunner(reconciler.RunnerOptions{
		Log:                ctrl.Log.WithName("reconciler"),
		DynamicClient:      dynamicClient,
		DiscoveryClient:    discoveryClient,
		AWSConfig:          awsConfig,
		SQSQueueURL:        sqsQueueURL,
		SQSWaitTimeSeconds: sqsWaitTimeSeconds,
		SQSMaxMessages:     sqsMaxMessages,
	})); err != nil {
		setupLog.Error(err, "unable to add reconciler runnable")
		os.Exit(1)
	}

	// Setup Infrastructure Controller for provisioning AWS resources
	if err = (&reconciler.SsmAnnotationResolverInfraReconciler{
		Client:    mgr.GetClient(),
		Log:       ctrl.Log.WithName("controllers").WithName("SsmAnnotationResolverInfra"),
		Scheme:    mgr.GetScheme(),
		AWSConfig: awsConfig,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "SsmAnnotationResolverInfra")
		os.Exit(1)
	}

	if err = mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")

		os.Exit(1)
	}
	if err = mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")

		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()

	if err = mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}

	setupLog.Info("manager exited")
}
