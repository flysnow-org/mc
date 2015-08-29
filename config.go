/*
 * Minio Client (C) 2015 Minio, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/minio/mc/pkg/console"
	"github.com/minio/minio/pkg/probe"
	"github.com/minio/minio/pkg/quick"
)

type configV2 struct {
	Version string
	Aliases map[string]string
	Hosts   map[string]hostConfig
}

// for backward compatibility
type configV101 configV2
type configV1 configV2

// cached variables should *NEVER* be accessed directly from outside this file.
var cache sync.Pool

// customConfigDir contains the whole path to config dir. Only access via get/set functions.
var mcCustomConfigDir string

// setMcConfigDir - construct minio client config folder.
func setMcConfigDir(configDir string) {
	mcCustomConfigDir = configDir
}

// getMcConfigDir - construct minio client config folder.
func getMcConfigDir() (string, *probe.Error) {
	if mcCustomConfigDir != "" {
		return mcCustomConfigDir, nil
	}
	u, err := user.Current()
	if err != nil {
		return "", probe.NewError(err)
	}
	// For windows the path is slightly different
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(u.HomeDir, globalMCConfigWindowsDir), nil
	default:
		return filepath.Join(u.HomeDir, globalMCConfigDir), nil
	}
}

// mustGetMcConfigDir - construct minio client config folder or fail
func mustGetMcConfigDir() (configDir string) {
	configDir, err := getMcConfigDir()
	fatalIf(err.Trace(), "Unable to get mcConfigDir.")

	return configDir
}

// createMcConfigDir - create minio client config folder
func createMcConfigDir() *probe.Error {
	p, err := getMcConfigDir()
	if err != nil {
		return err.Trace()
	}
	if err := os.MkdirAll(p, 0700); err != nil {
		return probe.NewError(err)
	}
	return nil
}

// getMcConfigPath - construct minio client configuration path
func getMcConfigPath() (string, *probe.Error) {
	dir, err := getMcConfigDir()
	if err != nil {
		return "", err.Trace()
	}
	return filepath.Join(dir, globalMCConfigFile), nil
}

// mustGetMcConfigPath - similar to getMcConfigPath, ignores errors
func mustGetMcConfigPath() string {
	path, err := getMcConfigPath()
	fatalIf(err.Trace(), "Unable to get mcConfigPath.")

	return path
}

// getMcConfig - reads configuration file and returns config
func getMcConfig() (*configV2, *probe.Error) {
	if !isMcConfigExists() {
		return nil, errInvalidArgument().Trace()
	}

	configFile, err := getMcConfigPath()
	if err != nil {
		return nil, err.Trace()
	}

	// Cached in private global variable.
	if v := cache.Get(); v != nil { // Use previously cached config.
		return v.(quick.Config).Data().(*configV2), nil
	}

	conf := newConfigV2()
	qconf, err := quick.New(conf)
	if err != nil {
		return nil, err.Trace()
	}

	err = qconf.Load(configFile)
	if err != nil {
		return nil, err.Trace()
	}
	cache.Put(qconf)
	return qconf.Data().(*configV2), nil

}

// mustGetMcConfig - reads configuration file and returns configs, exits on error
func mustGetMcConfig() *configV2 {
	config, err := getMcConfig()
	fatalIf(err.Trace(), "Unable to get mcConfig.")

	return config
}

// isMcConfigExists xreturns err if config doesn't exist
func isMcConfigExists() bool {
	configFile, err := getMcConfigPath()
	if err != nil {
		return false
	}
	if _, err := os.Stat(configFile); err != nil {
		return false
	}
	return true
}

// writeConfig - write configuration file
func writeConfig(config quick.Config) *probe.Error {
	if config == nil {
		return errInvalidArgument().Trace()
	}
	err := createMcConfigDir()
	if err != nil {
		return err.Trace()
	}
	configPath, err := getMcConfigPath()
	if err != nil {
		return err.Trace()
	}
	if err := config.Save(configPath); err != nil {
		return err.Trace()
	}
	return nil
}

func migrateConfig() {
	// Migrate config V1 to V101
	migrateConfigV1ToV101()
	// Migrate config V101 to V2
	migrateConfigV101ToV2()
}

func migrateConfigV1ToV101() {
	if !isMcConfigExists() {
		return
	}
	mcConfigV1, err := quick.Load(mustGetMcConfigPath(), newConfigV1())
	fatalIf(err.Trace(), "Unable to load config.")

	// update to newer version
	if mcConfigV1.Version() == "1.0.0" {
		confV101 := mcConfigV1.Data().(*configV1)
		confV101.Version = globalMCConfigVersion

		localHostConfig := hostConfig{}
		localHostConfig.AccessKeyID = ""
		localHostConfig.SecretAccessKey = ""

		s3HostConf := hostConfig{}
		s3HostConf.AccessKeyID = globalAccessKeyID
		s3HostConf.SecretAccessKey = globalSecretAccessKey

		if _, ok := confV101.Hosts["localhost:*"]; !ok {
			confV101.Hosts["localhost:*"] = localHostConfig
		}
		if _, ok := confV101.Hosts["127.0.0.1:*"]; !ok {
			confV101.Hosts["127.0.0.1:*"] = localHostConfig
		}
		if _, ok := confV101.Hosts["*.s3*.amazonaws.com"]; !ok {
			confV101.Hosts["*.s3*.amazonaws.com"] = s3HostConf
		}

		mcNewConfigV101, err := quick.New(confV101)
		fatalIf(err.Trace(), "Unable to initialize quick config.")

		err = mcNewConfigV101.Save(mustGetMcConfigPath())
		fatalIf(err.Trace(), "Unable to save config.")

		console.Infof("Successfully migrated %s from version ‘1.0.0’ to version ‘1.0.1’.\n", mustGetMcConfigPath())
	}
}

func migrateConfigV101ToV2() {
	if !isMcConfigExists() {
		return
	}
	mcConfigV101, err := quick.Load(mustGetMcConfigPath(), newConfigV101())
	fatalIf(err.Trace(), "Unable to load config.")

	// update to newer version
	if mcConfigV101.Version() == "1.0.1" {
		confV2 := mcConfigV101.Data().(*configV101)
		confV2.Version = globalMCConfigVersion

		mcNewConfigV2, err := quick.New(confV2)
		fatalIf(err.Trace(), "Unable to initialize quick config.")

		err = mcNewConfigV2.Save(mustGetMcConfigPath())
		fatalIf(err.Trace(), "Unable to save config.")

		console.Infof("Successfully migrated %s from version ‘1.0.1’ to version: ‘2’.\n", mustGetMcConfigPath())
	}
}

// newConfigV1() - get new config version 1.0.0
func newConfigV1() *configV1 {
	conf := new(configV1)
	conf.Version = "1.0.0"
	// make sure to allocate map's otherwise Golang
	// exits silently without providing any errors
	conf.Hosts = make(map[string]hostConfig)
	conf.Aliases = make(map[string]string)
	return conf
}

// newConfigV101() - get new config version 1.0.1
func newConfigV101() *configV101 {
	conf := new(configV101)
	conf.Version = "1.0.1"
	// make sure to allocate map's otherwise Golang
	// exits silently without providing any errors
	conf.Hosts = make(map[string]hostConfig)
	conf.Aliases = make(map[string]string)

	return conf
}

// newConfigV2 - get new config version 2
func newConfigV2() *configV2 {
	conf := new(configV2)
	conf.Version = globalMCConfigVersion
	// make sure to allocate map's otherwise Golang
	// exits silently without providing any errors
	conf.Hosts = make(map[string]hostConfig)
	conf.Aliases = make(map[string]string)

	localHostConfig := hostConfig{}
	localHostConfig.AccessKeyID = ""
	localHostConfig.SecretAccessKey = ""

	s3HostConf := hostConfig{}
	s3HostConf.AccessKeyID = globalAccessKeyID
	s3HostConf.SecretAccessKey = globalSecretAccessKey

	// Your example host config
	exampleHostConf := hostConfig{}
	exampleHostConf.AccessKeyID = globalAccessKeyID
	exampleHostConf.SecretAccessKey = globalSecretAccessKey

	playHostConfig := hostConfig{}
	playHostConfig.AccessKeyID = ""
	playHostConfig.SecretAccessKey = ""

	dlHostConfig := hostConfig{}
	dlHostConfig.AccessKeyID = ""
	dlHostConfig.SecretAccessKey = ""

	conf.Hosts[globalExampleHostURL] = exampleHostConf
	conf.Hosts["localhost:*"] = localHostConfig
	conf.Hosts["127.0.0.1:*"] = localHostConfig
	conf.Hosts["s3*.amazonaws.com"] = s3HostConf
	conf.Hosts["*.s3*.amazonaws.com"] = s3HostConf
	conf.Hosts["play.minio.io:9000"] = playHostConfig
	conf.Hosts["dl.minio.io:9000"] = dlHostConfig

	aliases := make(map[string]string)
	aliases["s3"] = "https://s3.amazonaws.com"
	aliases["play"] = "https://play.minio.io:9000"
	aliases["dl"] = "https://dl.minio.io:9000"
	aliases["localhost"] = "http://localhost:9000"
	conf.Aliases = aliases

	return conf
}

// newConfig - get new config interface
func newConfig() (config quick.Config, err *probe.Error) {
	conf := newConfigV2()
	config, err = quick.New(conf)
	if err != nil {
		return nil, err.Trace()
	}
	return config, nil
}
