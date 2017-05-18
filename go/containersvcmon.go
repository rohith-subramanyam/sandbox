/*
Package main implements a service monitor to run docker containers as a service/
daemon.

Copyright (c) 2017 Nutanix Inc. All rights reserved.

Author: rohith.subramanyam@nutanix.com
*/
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/golang/glog"

	"containersvc"
)

// Defining a custom flag value to store array of strings to accept multiple
// instances for some command-line flags.
// Example:- -port 127.0.0.1:80:8080 -port 127.0.0.1:6770:6770
type arrayFlags []string

// String implements the String function of flags.Value interface so that it can
// be used as a flag in the client.
func (af *arrayFlags) String() string {
	return fmt.Sprintf("%v", *af)
}

// Set implements the Set function of flags.Value interface so that it can be
// used as a flag in the client.
func (af *arrayFlags) Set(value string) error {
	*af = append(*af, value)

	return nil
}

// gflags.
var (
	ctrName   = flag.String("container-name", "", "Name of the container")
	ports     arrayFlags
	volDriver = flag.String("volume-driver", "",
		"Optional volume driver for the container")
	vols    arrayFlags
	bckgrnd = flag.Bool("background", false,
		"Run the container in the background")
	restartPolicy containersvc.RestartPolicyEnum = containersvc.No
	autoRm                                       = flag.Bool("auto-remove",
		true, "Automatically remove container when it exits")
	log       = flag.Bool("log", true, "Log the logs generated by the container")
	openStdin = flag.Bool("interactive", true,
		"Keep stdin open even if not attached")
	tty = flag.Bool("tty", true, "Attach standard streams to a tty, "+
		"including stdin if it is not closed.")
	oneCtr = flag.Bool("one-instance", true, "Only one container instance"+
		" of the image can be running.")
)

var stopMonitoring bool // Used to stop the runLoop.

// configure builds a containersvc.Config object from the flags and returns it.
func configure() *containersvc.Config {
	if glog.V(2) {
		glog.Info("Configuring containersvc")
	}

	portMap := make(map[string]string)
	for _, port := range ports {
		// port is in the format [host_ip]:host_port:container_port.
		// host_ip is optional.
		colSepIdx := strings.LastIndex(port, ":")
		hostPort := port[:colSepIdx]
		ctrPort := port[colSepIdx+1:]
		portMap[hostPort] = ctrPort
	}

	volsStrArr := []string(vols)

	cfg := &containersvc.Config{
		CtrName:       *ctrName,
		PortMap:       portMap,
		VolumeDriver:  *volDriver,
		Volumes:       volsStrArr,
		Background:    *bckgrnd,
		RestartPolicy: restartPolicy,
		AutoRemove:    autoRm,
		Log:           *log,
		OpenStdin:     openStdin,
		Tty:           tty,
		OnlyOneContainerInstancePerImage: *oneCtr,
	}

	glog.Infof("containersvc config: %s", containersvc.PPrint(cfg, true))

	return cfg
}

// runLoop is the monitoring loop where the container is started and is
// restarted if the container is stopped without the stop signals.
func runLoop(imgPath string, img string) {
	if glog.V(2) {
		glog.Info("Entering the run loop")
	}

	cfg := configure()

	for !stopMonitoring {
		if err := containersvc.Start(imgPath, img, cfg); err != nil {
			glog.Errorf("Failed to start container of image %s: "+
				"%s", img, err)
		}
		if !stopMonitoring {
			glog.Infof("Container of image %s exited. Restarting "+
				"it after 2 seconds...", img)
			time.Sleep(time.Second * 2)
		}
	}
	glog.Infof("Stopping monitoring of container of image: %s", img)
}

// stopSigHandler is the signal handler for signals that could be used to stop
// the container service: SIGINT, SIGQUIT, SIGTERM.
// It stops the container and terminates the run loop to gracefully exit.
// It runs as a goroutine and is waiting on the sigChan channel for the stop
// signals.
func stopSigHandler(sigChan chan os.Signal, img string) {
	if glog.V(2) {
		glog.Info("Waiting for stop signals...")
	}

	sig := <-sigChan

	glog.Infof("Received stop signal: %s", sig)

	stopMonitoring = true

	if err := containersvc.Stop(img, *ctrName, false); err != nil {
		glog.Errorf("Failed to stop container of image %s", img)
	}
}

// regStopSigHandler registers the signal handler for signals that could be used
// to stop the container service: SIGINT, SIGQUIT amd SIGTERM and starts the
// signal handler as a go routine.
func regStopSigHandler(img string) {
	if glog.V(2) {
		glog.Info("Registering signal handlers for stop signals: " +
			"SIGINT, SIGQUIT and SIGTERM")
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	go stopSigHandler(sigChan, img)
}

// validateCmdLnFlag validates command-line flags and is called for each flag
// that is set.
// In case of validation errors, it logs the error message, usage and exits with
// 255 exit code.
func validateCmdLnFlag(fl *flag.Flag) {
	if glog.V(2) {
		glog.Infof("> %s: %s", fl.Name, fl.Value)
	}

	if fl.Name == "port" {
		for _, port := range ports {
			if !strings.Contains(port, ":") {
				glog.Errorf("%s: invalid format: %s: %s", port,
					fl.Name, fl.Usage)
				os.Exit(255)
			}
		}
	} else if fl.Name == "volume" {
		for _, vol := range vols {
			if !strings.Contains(vol, ":") {
				glog.Errorf("%s: invalid format: %s: %s", vol,
					fl.Name, fl.Usage)
				os.Exit(255)
			}
		}
	} else if fl.Name == "restart-policy" {
		if !(restartPolicy == containersvc.No ||
			restartPolicy == containersvc.OnFailure ||
			restartPolicy == containersvc.UnlessStopped ||
			restartPolicy == containersvc.Always) {

			glog.Errorf("%s: invalid restart policy: %s: "+
				"%s", restartPolicy, fl.Name, fl.Usage)
			os.Exit(255)
		}
	}

}

// validateCmdLnFlags validates all command-line flags that are set.
func validateCmdLnFlags() {
	if glog.V(2) {
		glog.Info("Validating command-line flags that are set")
		glog.Info("Flags set:")
	}

	flag.Visit(validateCmdLnFlag)
}

// validateCmdLnArgs validates command-line positional arguments.
// In case of validation errors, it logs the error message, usage and exits with
// 255 exit code.
func validateCmdLnArgs(imgPath string) {
	if glog.V(2) {
		glog.Info("Validating command-line positional arguments: %s",
			imgPath)
	}

	if _, err := os.Stat(imgPath); err != nil {
		glog.Errorf("Error accessing %s: %s", imgPath, err)
		usage()
		os.Exit(255)
	}
}

// validateCmdLnArgsFlags validates command-line positional arguments and flags.
func validateCmdLnArgsFlags(imgPath string) {
	if glog.V(2) {
		glog.Infof("Validating command-line arguments %s and set flags",
			imgPath)
	}

	validateCmdLnArgs(imgPath)
	validateCmdLnFlags()
}

// parseCmdLnArgs parses the command-line positional arguments only (not
// command-line options/flags) and returns the 2 expected mandatory arguments:
// path to the docker image and the image name.
func parseCmdLnArgs(args []string) (string, string) {
	if glog.V(2) {
		glog.Infof("Parsing command-line arguments: %s", args)
	}

	if len(args) != 2 {
		glog.Errorf("Mandatory arguments: path_to_docker_image and/or" +
			" docker_image_name/ID not provided")
		usage()
		os.Exit(255)
	}

	imgPath := args[0]
	img := args[1]

	validateCmdLnArgsFlags(imgPath)

	return imgPath, img
}

// usage prints to stderr a usage message documenting all the defined
// command-line positional arguments and flags.
func usage() {
	fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "%s path_to_docker_image docker_image_name/ID\n",
		os.Args[0])
	flag.PrintDefaults()
}

// main parses the command-line flags, registers stop signal handlers and starts
// the run loop.
func main() {
	flag.Var(&ports, "port", "Port mapping(s) between host and container "+
		"in the format: [host_ip:]host_port:container_port")
	flag.Var(&vols, "volume", "Volumes to be mounted in the container in "+
		"the format: volume_name/host_path:container_path")
	flag.Var(&restartPolicy, "restart-policy", fmt.Sprintf("Restart policy "+
		"to be used for the container. Valid restart policies: %s, %s,"+
		" %s, %s", containersvc.No, containersvc.OnFailure,
		containersvc.UnlessStopped, containersvc.Always))
	flag.Usage = usage
	flag.Parse()
	flag.Set("logtostderr", "true")

	imgPath, img := parseCmdLnArgs(flag.Args())

	regStopSigHandler(img)

	runLoop(imgPath, img)

	glog.Flush()
}
