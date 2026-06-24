package controllers

import (
	"github.com/krateoplatformops/provider-runtime/pkg/controller"
	"github.com/krateoplatformops/provider-runtime/pkg/reconciler"
	ctrl "sigs.k8s.io/controller-runtime"

	repo "github.com/krateoplatformops/oasgen-provider/internal/controllers/restdefinition"
)

// Setup creates all controllers with the supplied logger and adds them to
// the supplied manager. metrics records the provider_runtime.reconcile.*
// telemetry suite and is a working no-op (nil) when OTEL_ENABLED is false.
func Setup(mgr ctrl.Manager, o controller.Options, metrics reconciler.MetricsRecorder) error {
	for _, setup := range []func(ctrl.Manager, controller.Options, reconciler.MetricsRecorder) error{
		repo.Setup,
	} {
		if err := setup(mgr, o, metrics); err != nil {
			return err
		}
	}
	return nil
}
