package processes

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"radio-stream-manager/internal/config"
	"radio-stream-manager/internal/types"
	"radio-stream-manager/internal/state"
	"radio-stream-manager/internal/templates"

	"go.uber.org/zap"
)

type Manager struct {
	config       *config.Config
	stateManager *state.DynamoDBManager
	logger       *zap.Logger
	processes    map[int]*ProcessInfo
	mutex        sync.RWMutex
}

type ProcessInfo struct {
	StreamID int
	Process  *os.Process
	Config   *templates.IcesConfig
	Started  time.Time
}

func NewManager(cfg *config.Config, stateManager *state.DynamoDBManager, logger *zap.Logger) *Manager {
	return &Manager{
		config:       cfg,
		stateManager: stateManager,
		logger:       logger,
		processes:    make(map[int]*ProcessInfo),
	}
}

func (m *Manager) StartStream(ctx context.Context, streamID int, config types.StreamConfig) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if _, exists := m.processes[streamID]; exists {
		m.logger.Warn("Stream already running", zap.Int("stream_id", streamID))
		return nil
	}

	// Generate ices configuration
	icesConfig := &templates.IcesConfig{
		StreamID:        streamID,
		StreamName:      config.Name,
		IcecastHost:     m.config.Icecast.Host,
		IcecastPort:     m.config.Icecast.Port,
		IcecastPassword: m.config.Icecast.Password,
		Genre:           config.Genre,
		Description:     config.Description,
		URL:             fmt.Sprintf("%s/s/%d", m.config.API.BaseURL, streamID),
	}

	logDir := fmt.Sprintf("%s/stream-%d", m.config.Ices.LogDir, streamID)
	configPath := fmt.Sprintf("%s/stream-%d.xml", m.config.Ices.ConfigDir, streamID)
	templatePath := filepath.Join(filepath.Dir(os.Args[0]), "ices-template.xml")

	// Set template values
	icesConfig.BaseDirectory = logDir
	icesConfig.Mountpoint = fmt.Sprintf("/stream-%d", streamID)
	if config.Premium {
		icesConfig.Bitrate = 128
	} else {
		icesConfig.Bitrate = 64
	}

	// Generate configuration file
	if err := templates.GenerateIcesConfig(icesConfig, templatePath, configPath); err != nil {
		return fmt.Errorf("failed to generate ices config: %w", err)
	}

	// Create log directory
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	// Start ices process
	cmd := exec.CommandContext(ctx, m.config.Ices.BinaryPath, "-c", configPath)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("STREAM_ID=%d", streamID),
		fmt.Sprintf("CLAQRADIO_STREAM_URL=%s", m.config.API.BaseURL),
		fmt.Sprintf("PERLLIB=%s/modules", m.config.Ices.ConfigDir),
	)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ices process: %w", err)
	}

	// Store process info
	processInfo := &ProcessInfo{
		StreamID: streamID,
		Process:  cmd.Process,
		Config:   icesConfig,
		Started:  time.Now(),
	}
	m.processes[streamID] = processInfo

	// Save to DynamoDB
	streamProcess := &state.StreamProcess{
		StreamID:      streamID,
		Status:        state.StatusRunning,
		ProcessID:     cmd.Process.Pid,
		ConfigPath:    configPath,
		Mountpoint:    icesConfig.Mountpoint,
		CreatedAt:     time.Now(),
		LastHeartbeat: time.Now(),
		Name:          config.Name,
		Premium:       config.Premium,
		Description:   config.Description,
		Genre:         config.Genre,
	}

	if err := m.stateManager.SaveProcess(ctx, streamProcess); err != nil {
		m.logger.Error("Failed to save process state", zap.Error(err))
	}

	m.logger.Info("Started stream",
		zap.Int("stream_id", streamID),
		zap.Int("pid", cmd.Process.Pid),
		zap.String("config_path", configPath),
	)
	m.logger.Debug("Stream process command and environment",
		zap.Int("stream_id", streamID),
		zap.String("command", cmd.String()),
		zap.Strings("env", cmd.Env),
	)

	// Monitor process in background
	go m.monitorProcess(streamID, cmd)

	return nil
}

func (m *Manager) StopStream(ctx context.Context, streamID int) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	processInfo, exists := m.processes[streamID]
	if !exists {
		m.logger.Warn("Stream not found", zap.Int("stream_id", streamID))
		return nil
	}

	// Terminate process
	if err := processInfo.Process.Signal(syscall.SIGTERM); err != nil {
		m.logger.Error("Failed to send SIGTERM", zap.Error(err), zap.Int("stream_id", streamID))
		// Force kill if SIGTERM fails
		if err := processInfo.Process.Kill(); err != nil {
			m.logger.Error("Failed to kill process", zap.Error(err), zap.Int("stream_id", streamID))
		}
	}

	// Clean up
	delete(m.processes, streamID)

	// Remove from DynamoDB
	if err := m.stateManager.DeleteProcess(ctx, streamID); err != nil {
		m.logger.Error("Failed to delete process state", zap.Error(err))
	}

	// Clean up config file
	configPath := fmt.Sprintf("%s/stream-%d.xml", m.config.Ices.ConfigDir, streamID)
	if err := os.Remove(configPath); err != nil {
		m.logger.Warn("Failed to remove config file", zap.Error(err), zap.String("config_path", configPath))
	}

	m.logger.Info("Stopped stream", zap.Int("stream_id", streamID))

	return nil
}

func (m *Manager) UpdateStream(ctx context.Context, streamID int, config types.StreamConfig) error {
	// For now, restart the stream with new configuration
	if err := m.StopStream(ctx, streamID); err != nil {
		return fmt.Errorf("failed to stop stream for update: %w", err)
	}

	time.Sleep(2 * time.Second) // Give time for cleanup

	return m.StartStream(ctx, streamID, config)
}

func (m *Manager) StopAll() error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	for streamID, processInfo := range m.processes {
		if err := processInfo.Process.Signal(syscall.SIGTERM); err != nil {
			m.logger.Error("Failed to stop process", zap.Error(err), zap.Int("stream_id", streamID))
		}
	}

	// Wait for processes to exit
	time.Sleep(5 * time.Second)

	// Force kill any remaining processes
	for streamID, processInfo := range m.processes {
		if err := processInfo.Process.Kill(); err != nil {
			m.logger.Error("Failed to kill process", zap.Error(err), zap.Int("stream_id", streamID))
		}
	}

	m.processes = make(map[int]*ProcessInfo)
	return nil
}

func (m *Manager) RestoreStreams(ctx context.Context) error {
	m.logger.Info("Restoring streams from state storage")

	processes, err := m.stateManager.ListProcesses(ctx)
	if err != nil {
		return fmt.Errorf("failed to list processes: %w", err)
	}

	for _, process := range processes {
		if process.Status != state.StatusRunning {
			continue
		}

		// Check if PID exists and is an ices process
		pid := process.ProcessID
		procPath := fmt.Sprintf("/proc/%d/cmdline", pid)
		cmdline, err := os.ReadFile(procPath)
		if err == nil && len(cmdline) > 0 && (string(cmdline) == m.config.Ices.BinaryPath || string(cmdline[:len(m.config.Ices.BinaryPath)]) == m.config.Ices.BinaryPath) {
			m.logger.Info("Reattaching to existing ices process",
				zap.Int("stream_id", process.StreamID),
				zap.Int("pid", pid),
				zap.String("name", process.Name),
			)
			processInfo := &ProcessInfo{
				StreamID: process.StreamID,
				Process:  &os.Process{Pid: pid},
				Config:   nil,
				Started:  process.CreatedAt,
			}
			m.processes[process.StreamID] = processInfo
			continue
		}

		m.logger.Info("Restoring stream",
			zap.Int("stream_id", process.StreamID),
			zap.String("name", process.Name),
		)

		config := types.StreamConfig{
			Name:        process.Name,
			Genre:       process.Genre,
			Description: process.Description,
			Premium:     process.Premium,
		}

		if err := m.StartStream(ctx, process.StreamID, config); err != nil {
			m.logger.Error("Failed to restore stream",
				zap.Error(err),
				zap.Int("stream_id", process.StreamID),
			)
			continue
		}
	}

	m.logger.Info("Stream restoration complete")
	return nil
}

func (m *Manager) StartHealthMonitor(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.healthCheck(ctx)
		}
	}
}

func (m *Manager) healthCheck(ctx context.Context) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	for streamID := range m.processes {
		if err := m.stateManager.UpdateHeartbeat(ctx, streamID); err != nil {
			m.logger.Error("Failed to update heartbeat", zap.Error(err), zap.Int("stream_id", streamID))
		}
	}
}

func (m *Manager) monitorProcess(streamID int, cmd *exec.Cmd) {
	err := cmd.Wait()
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Remove from active processes
	delete(m.processes, streamID)

	if err != nil {
		m.logger.Error("Process exited with error",
			zap.Error(err),
			zap.Int("stream_id", streamID),
		)
		// Attempt restart
		go func() {
			const maxRetries = 3
			const retryDelay = 5 * time.Second
			for i := 1; i <= maxRetries; i++ {
				m.logger.Info("Attempting to restart stream",
					zap.Int("stream_id", streamID),
					zap.Int("attempt", i),
				)
				// Try to get last known config from DynamoDB
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				streamProcess, err := m.stateManager.GetProcess(ctx, streamID)
				if err != nil || streamProcess == nil {
					m.logger.Error("Failed to get stream process for restart",
						zap.Error(err),
						zap.Int("stream_id", streamID),
					)
					time.Sleep(retryDelay)
					continue
				}
				// Reconstruct config for restart
				config := types.StreamConfig{
					Name:        streamProcess.Name,
					Genre:       streamProcess.Genre,
					Description: streamProcess.Description,
					Premium:     streamProcess.Premium,
				}
				restartErr := m.StartStream(ctx, streamID, config)
				if restartErr == nil {
					m.logger.Info("Successfully restarted stream",
						zap.Int("stream_id", streamID),
						zap.Int("attempt", i),
					)
					return
				}
				m.logger.Error("Restart attempt failed",
					zap.Error(restartErr),
					zap.Int("stream_id", streamID),
					zap.Int("attempt", i),
				)
				time.Sleep(retryDelay)
			}
			m.logger.Error("Failed to restart stream after max retries", zap.Int("stream_id", streamID))
		}()
	} else {
		m.logger.Info("Process exited cleanly", zap.Int("stream_id", streamID))
	}
}
