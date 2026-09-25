package app

import (
	"goteway/pkg/request"
	"goteway/static"

	"gopkg.in/yaml.v2"
)

type service struct {
	Path string `yaml:"path"`
	Host string `yaml:"host"`
}

type config struct {
	Server struct {
		Host string `yaml:"host"`
		Port string `yaml:"port"`
	} `yaml:"server"`
	Redis struct {
		Host string `yaml:"host"`
		Port string `yaml:"port"`
	} `yaml:"redis"`
	Services struct {
		Internal []service `yaml:"internal"`
		External []service `yaml:"external"`
	} `yaml:"services"`
}

func (c config) convertServicesToRequest() request.ServicesConfig {

	sc := request.ServicesConfig{}

	for _, s := range c.Services.Internal {
		sc.Internal = append(sc.Internal, request.ServiceConfig{
			Path: s.Path,
			Host: s.Host,
		})
	}

	for _, s := range c.Services.External {
		sc.External = append(sc.External, request.ServiceConfig{
			Path: s.Path,
			Host: s.Host,
		})
	}

	return sc
}

func loadConfig() *config {
	c := &config{}

	cFile := static.GetConfigFile()
	err := yaml.Unmarshal(cFile, c)

	if err != nil {
		panic(err)
	}

	return c
}
