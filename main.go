// Copyright 2026 Clastix Labs
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"fmt"
	"os"
	goruntime "runtime"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/clastix/go-controller-template/pkg"
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

	ctrlOpts := ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsBindAddress,
		},
		HealthProbeBindAddress:  healthProbeBindAddress,
		LeaderElection:          leaderElect,
		LeaderElectionNamespace: managerNamespace,
		LeaderElectionID:        "go-controller-template.clastix.io",
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrlOpts)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Add your controllers logic here:
	//if err = (&controllers.ResourceReconciler{Client: mgr.GetClient()}).SetupWithManager(mgr); err != nil {
	//	setupLog.Error(err, "unable to create controller", "controller", "Resource")
	//
	//	os.Exit(1)
	//}

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
