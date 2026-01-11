package main

import (
	middleware "platform-gateway/middlewares"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

func main() {

	/**
	* We have to define the operation for go/gin router
	* to work. By default we'll use the debug mode and
	* this will changed to release mode on the production
	* environments.
	 */
	gin.SetMode(gin.DebugMode)

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
	 * We need to return some CORS headers to fix issues with
	 * cross browser requests things.
	 */
	Router.Use(middleware.CORS())

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
	 * into the go-gin router
	 */
	Routes(Router)

	/**
	 * It's time to start the go/gin Router and make it available
	 * to our front facing application to make requests.
	 */
	Router.Run("0.0.0.0:8080")

}
