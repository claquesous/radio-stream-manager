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

func (m *Manager) StartStream(ctx context.Context, event types.StreamEvent) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	streamID := event.StreamID

	if _, exists := m.processes[streamID]; exists {
		m.logger.Warn("Stream already running", zap.Int("stream_id", streamID))
		return nil
	}

	// Generate ices configuration
	icesConfig := &templates.IcesConfig{
		StreamID:        streamID,
		StreamName:      event.Payload.Name,
		IcecastHost:     m.config.Icecast.Host,
		IcecastPort:     m.config.Icecast.Port,
		IcecastPassword: m.config.Icecast.Password,
		Genre:           event.Payload.Genre,
		Description:     event.Payload.Description,
		URL:             fmt.Sprintf("%s/s/%d", m.config.API.BaseURL, streamID),
	}

	logDir := fmt.Sprintf("%s/stream-%d", m.config.Ices.LogDir, streamID)
	configPath := fmt.Sprintf("%s/stream-%d.xml", m.config.Ices.ConfigDir, streamID)
	templatePath := filepath.Join(filepath.Dir(os.Args[0]), "ices-template.xml")

	// Set template values
	icesConfig.BaseDirectory = logDir
	icesConfig.Mountpoint = fmt.Sprintf("/stream-%d", streamID)
	if event.Payload.Premium {
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
	}

	if err := m.stateManager.SaveProcess(ctx, streamProcess); err != nil {
		m.logger.Error("Failed to save process state", zap.Error(err))
	}

	m.logger.Info("Started stream",
		zap.Int("stream_id", streamID),
		zap.Int("pid", cmd.Process.Pid),
		zap.String("config_path", configPath),
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

func (m *Manager) UpdateStream(ctx context.Context, event types.StreamEvent) error {
	// For now, restart the stream with new configuration
	if err := m.StopStream(ctx, event.StreamID); err != nil {
		return fmt.Errorf("failed to stop stream for update: %w", err)
	}

	time.Sleep(2 * time.Second) // Give time for cleanup

	return m.StartStream(ctx, event)
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
	} else {
		m.logger.Info("Process exited cleanly", zap.Int("stream_id", streamID))
	}
}
