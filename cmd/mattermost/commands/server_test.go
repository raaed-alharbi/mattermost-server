// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package commands

import (
	"io/ioutil"
	"net"
	"os"
	"syscall"
	"testing"

	"github.com/mattermost/mattermost-server/jobs"
	"github.com/mattermost/mattermost-server/model"
	"github.com/mattermost/mattermost-server/utils"
	"github.com/stretchr/testify/require"
)

type ServerTestHelper struct {
	configPath         string
	disableConfigWatch bool
	interruptChan      chan os.Signal
	originalInterval   int
	oldBuildNumber     string
}

func SetupServerTest() *ServerTestHelper {
	// Build a channel that will be used by the server to receive system signals…
	interruptChan := make(chan os.Signal, 1)
	// …and sent it immediately a SIGINT value.
	// This will make the server loop stop as soon as it started successfully.
	interruptChan <- syscall.SIGINT

	// Let jobs poll for termination every 0.2s (instead of every 15s by default)
	// Otherwise we would have to wait the whole polling duration before the test
	// terminates.
	originalInterval := jobs.DEFAULT_WATCHER_POLLING_INTERVAL
	jobs.DEFAULT_WATCHER_POLLING_INTERVAL = 200

	th := &ServerTestHelper{
		configPath:         utils.FindConfigFile("config.json"),
		disableConfigWatch: true,
		interruptChan:      interruptChan,
		originalInterval:   originalInterval,
	}

	// Run in dev mode so SiteURL gets set
	th.oldBuildNumber = model.BuildNumber
	model.BuildNumber = "dev"

	return th
}

func (th *ServerTestHelper) TearDownServerTest() {
	jobs.DEFAULT_WATCHER_POLLING_INTERVAL = th.originalInterval
	model.BuildNumber = th.oldBuildNumber
}

func TestRunServerSiteURL(t *testing.T) {
	th := SetupServerTest()
	defer th.TearDownServerTest()

	err := runServer(th.configPath, th.disableConfigWatch, false, th.interruptChan)
	require.NoError(t, err)
}

func TestRunServerInvalidConfigFile(t *testing.T) {
	th := SetupServerTest()
	defer th.TearDownServerTest()

	// Start the server with an unreadable config file
	unreadableConfigFile, err := ioutil.TempFile("", "mattermost-unreadable-config-file-")
	if err != nil {
		panic(err)
	}
	os.Chmod(unreadableConfigFile.Name(), 0200)
	defer os.Remove(unreadableConfigFile.Name())

	err = runServer(unreadableConfigFile.Name(), th.disableConfigWatch, false, th.interruptChan)
	require.Error(t, err)
}

func TestRunServerSystemdNotification(t *testing.T) {
	th := SetupServerTest()
	defer th.TearDownServerTest()

	// Get a random temporary filename for using as a mock systemd socket
	socketFile, err := ioutil.TempFile("", "mattermost-systemd-mock-socket-")
	if err != nil {
		panic(err)
	}
	socketPath := socketFile.Name()
	os.Remove(socketPath)

	// Set the socket path in the process environment
	originalSocket := os.Getenv("NOTIFY_SOCKET")
	os.Setenv("NOTIFY_SOCKET", socketPath)
	defer os.Setenv("NOTIFY_SOCKET", originalSocket)

	// Open the socket connection
	addr := &net.UnixAddr{
		Name: socketPath,
		Net:  "unixgram",
	}
	connection, err := net.ListenUnixgram("unixgram", addr)
	if err != nil {
		panic(err)
	}
	defer connection.Close()
	defer os.Remove(socketPath)

	// Listen for socket data
	socketReader := make(chan string)
	go func(ch chan string) {
		buffer := make([]byte, 512)
		count, err := connection.Read(buffer)
		if err != nil {
			panic(err)
		}
		data := buffer[0:count]
		ch <- string(data)
	}(socketReader)

	// Start and stop the server
	err = runServer(th.configPath, th.disableConfigWatch, false, th.interruptChan)
	require.NoError(t, err)

	// Ensure the notification has been sent on the socket and is correct
	notification := <-socketReader
	require.Equal(t, notification, "READY=1")
}

func TestRunServerNoSystemd(t *testing.T) {
	th := SetupServerTest()
	defer th.TearDownServerTest()

	// Temporarily remove any Systemd socket defined in the environment
	originalSocket := os.Getenv("NOTIFY_SOCKET")
	os.Unsetenv("NOTIFY_SOCKET")
	defer os.Setenv("NOTIFY_SOCKET", originalSocket)

	err := runServer(th.configPath, th.disableConfigWatch, false, th.interruptChan)
	require.NoError(t, err)
}
