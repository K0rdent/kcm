// Copyright 2024
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package backup

import (
	"context"
	"fmt"
	"time"

	cron "github.com/robfig/cron/v3"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kcmv1alpha1 "github.com/K0rdent/kcm/api/v1alpha1"
)

const scheduleMgmtNameLabel = "k0rdent.mirantis.com/management-backup"

func (r *Reconciler) ReconcileBackup(ctx context.Context, mgmtBackup *kcmv1alpha1.ManagementBackup, cronRaw string) (ctrl.Result, error) {
	if mgmtBackup == nil {
		return ctrl.Result{}, nil
	}

	l := ctrl.LoggerFrom(ctx)

	if mgmtBackup.IsSchedule() && mgmtBackup.CreationTimestamp.IsZero() || mgmtBackup.UID == "" {
		l.Info("Creating scheduled ManagementBackup")
		return r.createManagementBackup(ctx, mgmtBackup)
	}

	mgmtBackup.Status.Paused = false

	// schedule-creation path
	if mgmtBackup.IsSchedule() {
		cronSchedule, err := cron.ParseStandard(cronRaw)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to parse cron schedule %s: %w", cronRaw, err)
		}

		isDue, nextAttemptTime := getNextAttemptTime(mgmtBackup, cronSchedule)

		// here we can put as many conditions as we want, e.g. if upgrade is progressing
		isOkayToCreateBackup := isDue && !r.isVeleroBackupIsInProgress(ctx, mgmtBackup)

		if isOkayToCreateBackup {
			return r.createScheduleBackup(ctx, mgmtBackup, nextAttemptTime)
		}

		newNextAttemptTime := &metav1.Time{Time: nextAttemptTime}
		if !mgmtBackup.Status.NextAttempt.Equal(newNextAttemptTime) {
			mgmtBackup.Status.NextAttempt = newNextAttemptTime

			if err := r.cl.Status().Update(ctx, mgmtBackup); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update ManagementBackup %s status with next attempt time: %w", mgmtBackup.Name, err)
			}
		}

		if mgmtBackup.Status.LastBackupName == "" { // is not due, nothing to do
			return ctrl.Result{}, nil
		}
	} else if mgmtBackup.Status.LastBackupName == "" { // single mgmtbackup, velero backup has not been created yet
		return r.createSingleBackup(ctx, mgmtBackup)
	}

	l.V(1).Info("Collecting backup status")

	backupName := mgmtBackup.Name
	if mgmtBackup.IsSchedule() {
		backupName = mgmtBackup.Status.LastBackupName
	}
	veleroBackup := new(velerov1.Backup)
	if err := r.cl.Get(ctx, client.ObjectKey{
		Name:      backupName,
		Namespace: r.systemNamespace,
	}, veleroBackup); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get velero Backup: %w", err)
	}

	l.V(1).Info("Updating backup status")
	mgmtBackup.Status.LastBackup = &veleroBackup.Status
	if err := r.cl.Status().Update(ctx, mgmtBackup); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update ManagementBackup %s status: %w", mgmtBackup.Name, err)
	}

	return ctrl.Result{}, nil
}

func (r *Reconciler) createManagementBackup(ctx context.Context, mgmtBackup *kcmv1alpha1.ManagementBackup) (ctrl.Result, error) {
	if mgmtBackup.Annotations == nil {
		mgmtBackup.Annotations = make(map[string]string)
	}
	mgmtBackup.Annotations[kcmv1alpha1.ScheduleBackupAnnotation] = "true"

	if err := r.cl.Create(ctx, mgmtBackup); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create scheduled ManagementBackup: %w", err)
	}

	return ctrl.Result{}, nil
}

func (r *Reconciler) createScheduleBackup(ctx context.Context, mgmtBackup *kcmv1alpha1.ManagementBackup, nextAttemptTime time.Time) (ctrl.Result, error) {
	now := time.Now()
	backupName := mgmtBackup.TimestampedBackupName(now)

	// TODO: NOTE: should we also set the ownership for the scheduled backups? i don't wanna do this to avoid accidental backups removal
	// in general this affects how fast the status is being updated (on event with the owner or via the periodic runner event without the owner)
	if err := r.createNewVeleroBackup(ctx, backupName, withScheduleLabel(mgmtBackup.Name)); err != nil {
		return ctrl.Result{}, err
	}

	mgmtBackup.Status.LastBackupName = backupName
	mgmtBackup.Status.LastBackupTime = &metav1.Time{Time: now}
	mgmtBackup.Status.NextAttempt = &metav1.Time{Time: nextAttemptTime}

	if err := r.cl.Status().Update(ctx, mgmtBackup); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update ManagementBackup %s status: %w", mgmtBackup.Name, err)
	}

	return ctrl.Result{}, nil
}

func (r *Reconciler) createSingleBackup(ctx context.Context, mgmtBackup *kcmv1alpha1.ManagementBackup) (ctrl.Result, error) {
	if err := r.createNewVeleroBackup(ctx, mgmtBackup.Name, withSetControllerReference(mgmtBackup, r.scheme)); err != nil {
		return ctrl.Result{}, err
	}

	mgmtBackup.Status.LastBackupName = mgmtBackup.Name
	mgmtBackup.Status.LastBackupTime = &metav1.Time{Time: time.Now()}

	if err := r.cl.Status().Update(ctx, mgmtBackup); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update ManagementBackup %s status: %w", mgmtBackup.Name, err)
	}

	return ctrl.Result{}, nil
}

type createOpt func(*velerov1.Backup)

func withScheduleLabel(scheduleName string) createOpt {
	return func(b *velerov1.Backup) {
		if b.Labels == nil {
			b.Labels = make(map[string]string)
		}
		b.Labels[scheduleMgmtNameLabel] = scheduleName
	}
}

func withSetControllerReference(mgmtBackup *kcmv1alpha1.ManagementBackup, scheme *runtime.Scheme) createOpt {
	return func(b *velerov1.Backup) {
		_ = controllerutil.SetControllerReference(mgmtBackup, b, scheme)
	}
}

func (r *Reconciler) createNewVeleroBackup(ctx context.Context, backupName string, createOpts ...createOpt) error {
	l := ctrl.LoggerFrom(ctx)

	veleroBackup, err := r.getNewVeleroBackup(ctx, backupName)
	if err != nil {
		return err
	}

	for _, o := range createOpts {
		o(veleroBackup)
	}

	if err := r.cl.Create(ctx, veleroBackup); client.IgnoreAlreadyExists(err) != nil { // avoid err-loop on status update error
		return fmt.Errorf("failed to create velero Backup: %w", err)
	}

	l.V(1).Info("Initial backup has been created", "new_backup_name", client.ObjectKeyFromObject(veleroBackup))
	return nil
}

func (r *Reconciler) getNewVeleroBackup(ctx context.Context, backupName string) (*velerov1.Backup, error) {
	templateSpec, err := getBackupTemplateSpec(ctx, r.cl)
	if err != nil {
		return nil, fmt.Errorf("failed to construct velero backup spec: %w", err)
	}

	veleroBackup := &velerov1.Backup{
		TypeMeta: metav1.TypeMeta{
			APIVersion: velerov1.SchemeGroupVersion.String(),
			Kind:       "Backup",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      backupName,
			Namespace: r.systemNamespace,
		},
		Spec: *templateSpec,
	}

	return veleroBackup, nil
}

func (r *Reconciler) isVeleroBackupIsInProgress(ctx context.Context, schedule *kcmv1alpha1.ManagementBackup) bool {
	backups := &velerov1.BackupList{}
	if err := r.cl.List(ctx, backups, client.InNamespace(r.systemNamespace), client.MatchingLabels{scheduleMgmtNameLabel: schedule.Name}); err != nil {
		return true
	}

	for _, backup := range backups.Items {
		if backup.Status.Phase == velerov1.BackupPhaseNew ||
			backup.Status.Phase == velerov1.BackupPhaseInProgress {
			return true
		}
	}

	return false
}

func getNextAttemptTime(schedule *kcmv1alpha1.ManagementBackup, cronSchedule cron.Schedule) (bool, time.Time) {
	lastBackupTime := schedule.CreationTimestamp.Time
	if schedule.Status.LastBackup != nil {
		lastBackupTime = schedule.Status.LastBackupTime.Time
	}

	nextAttemptTime := cronSchedule.Next(lastBackupTime) // might be in past so rely on now
	now := time.Now()
	isDue := now.After(nextAttemptTime)
	if isDue {
		nextAttemptTime = now
	}

	return isDue, nextAttemptTime
}
