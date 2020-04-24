package knockrd

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/kayac/go-config"
	"github.com/natureglobal/realip"
)

const (
	DefaultPort     = 9876
	DefaultTable    = "knockrd"
	DefaultTTL      = time.Hour
	DefaultCacheTTL = 10 * time.Second
)

var DefaultRealIPFrom = []string{
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"127.0.0.0/8",
	"fe80::/10",
	"::1/128",
}

type Config struct {
	Port         int           `yaml:"port"`
	TableName    string        `yaml:"table_name"`
	RealIPFrom   []string      `yaml:"real_ip_from"`
	RealIPHeader string        `yaml:"real_ip_header"`
	TTL          time.Duration `yaml:"ttl"`
	CacheTTL     time.Duration `yaml:"cache_ttl"`
	AWS          AWSConfig     `yaml:"aws"`
	IPSet        struct {
		V4 IPSetConfig `yaml:"v4"`
		V6 IPSetConfig `yaml:"v6"`
	} `yaml:"ip-set"`
}

type AWSConfig struct {
	Region   string `yaml:"region"`
	Endpoint string `yaml:"endpoint"`
}

type IPSetConfig struct {
	ID    string `yaml:"id"`
	Scope string `yaml:"scope"`
	Name  string `yaml:"name"`
}

func LoadConfig(path string) (*Config, error) {
	log.Println("[info] loading config file", path)
	c := Config{
		Port:         DefaultPort,
		TableName:    DefaultTable,
		RealIPFrom:   DefaultRealIPFrom,
		RealIPHeader: realip.HeaderXForwardedFor,
		TTL:          DefaultTTL,
		CacheTTL:     DefaultCacheTTL,
		AWS: AWSConfig{
			Region:   os.Getenv("AWS_REGION"),
			Endpoint: os.Getenv("AWS_ENDPOINT"),
		},
	}
	if path == "" {
		return &c, nil
	}
	err := config.LoadWithEnv(&c, path)
	if err != nil {
		log.Println("[debug]", c.String())
	}
	return &c, err
}

func (c *Config) String() string {
	b, _ := json.Marshal(c)
	return string(b)
}

// Setup setups resources by config
func (c *Config) Setup() (http.Handler, func(context.Context, events.DynamoDBEvent) error, error) {
	log.Println("[info] setup")
	onLambda := strings.HasPrefix(os.Getenv("AWS_EXECUTION_ENV"), "AWS_Lambda_go")
	if onLambda {
		// Allows RemoteAddr set by lambdaHandler.ServeHTTP()
		c.RealIPFrom = append(c.RealIPFrom, "127.0.0.1/32")
	}
	var ipfroms []*net.IPNet
	for _, cidr := range c.RealIPFrom {
		_, ipnet, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, nil, err
		}
		ipfroms = append(ipfroms, ipnet)
	}

	middleware := realip.MustMiddleware(&realip.Config{
		RealIPFrom:      ipfroms,
		RealIPHeader:    c.RealIPHeader,
		RealIPRecursive: true,
	})
	hh := middleware(mux)
	if onLambda {
		hh = lambdaHandler{hh}
	}

	sh := newStreamHandler(c)

	b, err := NewDynamoDBBackend(c)
	if err != nil {
		return nil, nil, err
	}
	if c.CacheTTL > 0 {
		if c.CacheTTL > c.TTL {
			log.Printf(
				"[warn] cahce_ttl(%s) is longer than ttl(%s). set cache_ttl equals to ttl.",
				c.CacheTTL,
				c.TTL,
			)
		}
		c.CacheTTL = c.TTL
		var err error
		backend, err = NewCachedBackend(b, c.CacheTTL)
		if err != nil {
			return nil, nil, err
		}
	}
	return hh, sh, err
}
