package main

import (
	"log"
	"os"
	middleware "entriq/middlewares"
	"reflect"
	"strings"

	"entriq/internal/config"
	"entriq/internal/gateway"
	intmiddleware "entriq/internal/middleware"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

func main() {

	/**
	 * Set gin mode from GIN_MODE environment variable.
	 * Defaults to release mode for safe production deployments.
	 * Set GIN_MODE=debug locally for verbose output.
	 */
	ginMode := os.Getenv("GIN_MODE")
	if ginMode == "" {
		ginMode = gin.ReleaseMode
	}
	gin.SetMode(ginMode)

	/**
	 * Load gateway configuration from file
	 * Checks CONFIG_PATH env var, defaults to ./config/entriq.yaml
	 */
	log.Printf("Entriq gateway version %s", Version)
	config.AppVersion = Version
	gatewayConfig, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load gateway config: %v", err)
	}
	log.Printf("Gateway config loaded successfully")

	/**
	 * Initialize gateway with configuration
	 */
	gw, err := gateway.New(gatewayConfig)
	if err != nil {
		log.Fatalf("Failed to initialize gateway: %v", err)
	}
	log.Printf("Gateway initialized successfully")

	/**
	 * Here we'll create go/gin Router handler to take care about
	 * the routing inside in the application. There will be no
	 * middlewares in the init stage.
	 */
	Router := gin.New()
	Router.SetTrustedProxies(nil)

	/**
	 * Logger middleware will write the logs to gin.DefaultWriter
	 * We are supposed to use some custom made Logger to make log
	 * rotate. But this will be here for while
	 */
	Router.Use(gin.Logger())

	/**
	 * We have to make our application immune to unexpected 500
	 * server side errors. This Recovery middleware will recovers
	 * from any panics and writes a 500 if there was one.
	 */
	Router.Use(gin.Recovery())

	/**
	 * RequestID middleware generates a unique UUID for each request
	 * and sets it as X-Request-ID header. This flows to all downstream
	 * services (auth service, backend services) for request tracing.
	 */
	Router.Use(intmiddleware.RequestID())

	/**
	 * We need to return some CORS headers to fix issues with
	 * cross browser requests things.
	 */
	Router.Use(middleware.CORS(gatewayConfig.CORS))

	/**
	* Extend the capabilities of the Validator engine to return
	* JSON tag name when we query for Field instead of the Struct
	* field name
	 */
	if validator, ok := binding.Validator.Engine().(*validator.Validate); ok {
		validator.RegisterTagNameFunc(func(field reflect.StructField) string {
			name := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
			return name
		})
	}

	/**
	 * This is where we include all the routes define in our application
	 * into the go-gin router (pass gateway instance)
	 */
	Routes(Router, gw)

	/**
	 * It's time to start the go/gin Router and make it available
	 * to our front facing application to make requests.
	 */
	Router.Run("0.0.0.0:8080")

}
