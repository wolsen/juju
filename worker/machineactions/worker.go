// Copyright 2016 Canonical Ltd.
// Copyright 2016 Cloudbase Solutions
// Licensed under the AGPLv3, see LICENCE file for details.

package machineactions

import (
	"fmt"
	"os"
	"time"

	"github.com/juju/errors"
	"github.com/juju/juju/api/machineactions"
	"github.com/juju/juju/apiserver/params"
	"github.com/juju/juju/core/actions"
	"github.com/juju/juju/watcher"
	"github.com/juju/juju/worker"
	"github.com/juju/loggo"
	"github.com/juju/names"
	"github.com/juju/utils/clock"
	"github.com/juju/utils/exec"
)

var logger = loggo.GetLogger("juju.worker.machineactions")

// Facade defines the capabilities required by the worker from the API.
type Facade interface {
	WatchActionNotifications(agent names.Tag) (watcher.StringsWatcher, error)
	RunningActions(agent names.Tag) ([]params.ActionResult, error)

	Action(names.ActionTag) (*machineactions.Action, error)
	ActionBegin(names.ActionTag) error
	ActionFinish(tag names.ActionTag, status string, results map[string]interface{}, message string) error
}

// WorkerConfig defines the worker's dependencies.
type WorkerConfig struct {
	Facade       Facade
	AgentTag     names.Tag
	HandleAction func(name string, params map[string]interface{}) (results map[string]interface{}, err error)
}

// Validate returns an error if the configuration is not complete.
func (c WorkerConfig) Validate() error {
	if c.Facade == nil {
		return errors.NotValidf("nil Facade")
	}
	if c.AgentTag == nil {
		return errors.NotValidf("nil AgentTag")
	}
	if c.HandleAction == nil {
		return errors.NotValidf("nil HandleAction")
	}
	return nil
}

// NewMachineActionsWorker returns a worker.Worker that watches for actions
// enqueued on this machine and tries to execute them.
func NewMachineActionsWorker(config WorkerConfig) (worker.Worker, error) {
	if err := config.Validate(); err != nil {
		return nil, errors.Trace(err)
	}
	swConfig := watcher.StringsConfig{
		Handler: &handler{config},
	}
	return watcher.NewStringsWorker(swConfig)
}

// handler implements watcher.StringsHandler
type handler struct {
	config WorkerConfig
}

// SetUp is part of the watcher.StringsHandler interface.
func (h *handler) SetUp() (watcher.StringsWatcher, error) {
	actions, err := h.config.Facade.RunningActions(h.config.AgentTag)
	if err != nil {
		return nil, errors.Trace(err)
	}
	// We try to cancel any running action before starting up so actions don't linger around
	// We *should* really have only one action coming up here if the execution is serial but
	// this is best effort anyway.
	for _, action := range actions {
		tag, err := names.ParseActionTag(action.Action.Tag)
		if err != nil {
			logger.Infof("tried to cancel action %s but failed with error %v", action.Action.Tag, err)
			continue
		}
		err = h.config.Facade.ActionFinish(tag, params.ActionCancelled, nil, "action cancelled")
		if err != nil {
			logger.Infof("tried to cancel action %s but failed with error %v", action.Action.Tag, err)
		}
	}
	return h.config.Facade.WatchActionNotifications(h.config.AgentTag)
}

// Handle is part of the watcher.StringsHandler interface.
// It should give us any actions currently enqueued for this machine.
// We try to execute every action before returning
func (h *handler) Handle(_ <-chan struct{}, actionsSlice []string) error {
	for _, actionId := range actionsSlice {
		ok := names.IsValidAction(actionId)
		if !ok {
			errors.Errorf("got invalid action id %s", actionId)
		}

		actionTag := names.NewActionTag(actionId)
		action, err := h.config.Facade.Action(actionTag)
		if err != nil {
			return errors.Errorf("could not retrieve action %s", actionId)
		}

		// We don't have to revalidate params here since they can't be stored in state when invalid.
		name := action.Name()
		actionParams := action.Params()

		err = h.config.Facade.ActionBegin(actionTag)
		if err != nil {
			return errors.Errorf("could not begin action %s", name)
		}

		results, err := h.config.HandleAction(action.Name(), action.Params())
		if err != nil {
			return h.config.Facade.ActionFinish(actionTag, params.ActionCancelled, nil, err.Error())
		}
		return h.config.Facade.ActionFinish(actionTag, params.ActionCompleted, results, "")
	}
	return nil
}

// TearDown is part of the watcher.NotifyHandler interface.
func (h *handler) TearDown() error {
	// Nothing to cleanup, only state is the watcher
	return nil
}

func HandleAction(name string, params map[string]interface{}) (results map[string]interface{}, err error) {
	if name == actions.JujuRunActionName {
		return handleJujuRunAction(params)
	}
	return nil, errors.Errorf("unexpected action %s", name)
}

func handleJujuRunAction(params map[string]interface{}) (results map[string]interface{}, err error) {
	// The spec checks that the parameters are available so we don't need to check again here
	command, _ := params["command"].(string)

	// The timeout is passed in in nanoseconds(which are represented in go as int64)
	// But due to serialization it comes out as float64
	timeout, _ := params["timeout"].(float64)

	res, err := runCommandWithTimeout(command, time.Duration(timeout), clock.WallClock)

	actionResults := map[string]interface{}{}
	if res != nil {
		actionResults["Code"] = res.Code
		actionResults["Stdout"] = fmt.Sprintf("%s", res.Stdout)
		actionResults["Stderr"] = fmt.Sprintf("%s", res.Stderr)
	}
	return actionResults, err
}

func runCommandWithTimeout(command string, timeout time.Duration, clock clock.Clock) (*exec.ExecResponse, error) {
	cmd := exec.RunParams{
		Commands:    command,
		Environment: os.Environ(),
		Clock:       clock,
	}

	err := cmd.Run()
	if err != nil {
		return nil, errors.Trace(err)
	}

	var cancel chan struct{}
	if timeout != 0 {
		cancel = make(chan struct{})
		go func() {
			<-clock.After(timeout)
			close(cancel)
		}()
	}

	return cmd.WaitWithCancel(cancel)
}
