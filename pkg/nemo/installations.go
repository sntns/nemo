package nemo

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/storage"
)

// Installations returns the installations of the stack, as nemo keeps them.
func (nemo *Nemo) Installations(ctx context.Context, stack string) (_ []storage.Installation, err error) {
	done, err := nemo.open(ctx, nil, stack, false)
	if err != nil {
		return nil, err
	}
	defer release(done, &err)
	return nemo.records(stack).ListInstallations(ctx, stack)
}

// Installation returns an installation, as nemo keeps it.
func (nemo *Nemo) Installation(ctx context.Context, stack, name string) (_ storage.Installation, err error) {
	done, err := nemo.open(ctx, nil, stack, false)
	if err != nil {
		return storage.Installation{}, err
	}
	defer release(done, &err)
	return nemo.records(stack).GetInstallation(ctx, stack, name)
}

// Runs returns the actions of an installation, oldest first, each as up
// and the actions return it: its claim, with its result and outputs.
func (nemo *Nemo) Runs(ctx context.Context, stack, name string) (_ []Run, err error) {
	done, err := nemo.open(ctx, nil, stack, false)
	if err != nil {
		return nil, err
	}
	defer release(done, &err)
	records := nemo.records(stack)
	claims, err := records.ListRuns(ctx, stack, name)
	if err != nil {
		return nil, err
	}
	results, err := records.ListResults(ctx, stack, name)
	if err != nil {
		return nil, err
	}
	runs := make([]Run, 0, len(claims))
	for _, claim := range claims {
		var custom storage.RunCustom
		if data, err := json.Marshal(claim.Custom); err == nil {
			_ = json.Unmarshal(data, &custom)
		}
		run := Run{
			Installation: claim.Installation,
			Revision:     claim.Revision,
			Action:       claim.Action,
			Bundle:       BundleVersion{Name: claim.Bundle.Name, Version: claim.Bundle.Version},
			Target:       custom.Target,
			Driver:       custom.Installer.DriverName(),
			Parameters:   claim.Parameters,
			Started:      claim.Created,
		}
		for _, credential := range custom.Credentials {
			run.Credentials = append(run.Credentials, credential.Name)
		}
		if index := slices.IndexFunc(results, func(result cnab.Result) bool { return result.ClaimID == claim.ID }); index >= 0 {
			result := results[index]
			run.Status, run.Ended = result.Status, result.Created
			if run.Outputs, err = records.GetOutputs(ctx, stack, name, result.ID); err != nil {
				return nil, err
			}
		}
		runs = append(runs, run)
	}
	return runs, nil
}
