package main

import (
	"log"
	"net/http"

	"hivepanel-worker/internal/allocation"
	"hivepanel-worker/internal/api"
	"hivepanel-worker/internal/backup"
	"hivepanel-worker/internal/cell"
	"hivepanel-worker/internal/comb"
	"hivepanel-worker/internal/config"
	"hivepanel-worker/internal/panel"
	hiveruntime "hivepanel-worker/internal/runtime"
	dockerruntime "hivepanel-worker/internal/runtime/docker"
	processruntime "hivepanel-worker/internal/runtime/process"
	workersftp "hivepanel-worker/internal/sftp"
)

func main() {
	cfg := config.Load()

	allocManager := allocation.NewManager(
		cfg.Allocations.IP,
		cfg.Allocations.PortStart,
		cfg.Allocations.PortEnd,
	)

	combManager := comb.NewManager(cfg.Paths.Data)
	if err := combManager.Load(); err != nil {
		log.Fatal(err)
	}

	var workerRuntime hiveruntime.Runtime

	switch cfg.Runtime.Type {
	case "process":
		workerRuntime = processruntime.New()

	case "docker":
		dockerRuntime, err := dockerruntime.New(cfg.Docker.Network)
		if err != nil {
			log.Fatal(err)
		}

		workerRuntime = dockerRuntime

	default:
		log.Fatal("unknown runtime type: " + cfg.Runtime.Type)
	}

	backupManager := backup.NewManager(cfg.Paths.Backups)

	cellManager := cell.NewManager(
		cfg.Paths.Data,
		cfg.Paths.Instances,
		combManager,
		workerRuntime,
		allocManager,
		backupManager,
	)

	if err := cellManager.Load(); err != nil {
		log.Fatal(err)
	}

	if err := cellManager.RecoverRuntime(); err != nil {
		log.Fatal(err)
	}

	panel.StartHeartbeat(cfg)

	if cfg.SFTP.Enabled {
		authClient := panel.NewSFTPAuthClient(cfg)
		handlerFactory := workersftp.NewJailHandlerFactory()

		sftpServer, err := workersftp.NewServer(
			cfg,
			authClient,
			handlerFactory,
		)
		if err != nil {
			log.Fatal("failed to initialise SFTP server: ", err)
		}

		go func() {
			log.Println(
				"HivePanel SFTP server running on " +
					cfg.SFTP.Listen,
			)

			if err := sftpServer.ListenAndServe(); err != nil {
				log.Fatal("SFTP server stopped: ", err)
			}
		}()
	} else {
		log.Println("HivePanel SFTP server is disabled")
	}

	router := api.NewRouter(
		cfg,
		cellManager,
		combManager,
	)

	log.Println(
		"HivePanel Worker running on " +
			cfg.Worker.Listen,
	)

	log.Println(
		"Config loaded from " +
			cfg.ConfigPath,
	)

	log.Fatal(
		http.ListenAndServe(
			cfg.Worker.Listen,
			router,
		),
	)
}
