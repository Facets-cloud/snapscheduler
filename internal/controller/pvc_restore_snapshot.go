package controller

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v4/apis/volumesnapshot/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	restorePvc         = "snapscheduler.backube/restore-from"
	PvcRequeueTime     = 30 * time.Second
	VolumeSnapshotKind = "VolumeSnapshot"
)

// create pvc reconciler
type PvcReconciler struct {
	Client client.Client
	Scheme *runtime.Scheme
}

// SetupWithManager sets up the controller with the Manager.
func (r *PvcReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.PersistentVolumeClaim{}).
		Complete(r)
}

// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;update;delete
//
//nolint:lll
func (r *PvcReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	reqLogger := log.FromContext(ctx).WithValues("pvcsnapshot-snapscheduler", req.NamespacedName)
	reqLogger.Info("Reconciling PvcSnapshot-Snapscheduler")
	pvcInstance := &corev1.PersistentVolumeClaim{}
	err := r.Client.Get(ctx, req.NamespacedName, pvcInstance)
	if err != nil {
		if kerrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Return and don't requeue
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}
	result, err := checkPvcToRestore(ctx, pvcInstance, reqLogger, r.Client)
	return result, err
}

func checkPvcToRestore(ctx context.Context, schedule *corev1.PersistentVolumeClaim,
	logger logr.Logger, c client.Client) (ctrl.Result, error) {
	restoreAnnotation := restorePvc
	if val, ok := schedule.Annotations[restoreAnnotation]; ok {
		// Copy the existing PVC template
		oldPvc := schedule.DeepCopy()
		// Remove old pv name
		oldPvc.Spec.VolumeName = ""
		// Delete the existing PVC
		err := c.Delete(ctx, schedule)
		if err != nil {
			logger.Error(err, "Failed to delete PVC", "PVC", schedule.Name)
			return ctrl.Result{}, err
		}
		// Wait for PVC to be deleted
		// Remove finalizer to allow PVC deletion
		if len(schedule.GetFinalizers()) > 0 {
			schedule.SetFinalizers(nil)
			if err := c.Update(ctx, schedule); err != nil {
				logger.Error(err, "Failed to remove finalizers from PVC", "PVC", schedule.Name)
				return ctrl.Result{}, err
			}
		}
		time.Sleep(time.Second)
		// Recreate the PVC with datasource attached to it
		newPvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      oldPvc.Name,
				Namespace: oldPvc.Namespace,
			},
			Spec: oldPvc.Spec,
		}
		newPvc.Spec.DataSource = &corev1.TypedLocalObjectReference{
			APIGroup: &snapv1.SchemeGroupVersion.Group,
			Kind:     VolumeSnapshotKind,
			Name:     val,
		}
		err = c.Create(ctx, newPvc)
		if err != nil {
			logger.Error(err, "Failed to create PVC", "PVC", newPvc.Name)
			return ctrl.Result{}, err
		}
		logger.Info("PVC created with restore annotation from snapscheduler", "PVC", newPvc.Name)
	}
	return ctrl.Result{}, nil
}
