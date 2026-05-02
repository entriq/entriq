package main

import (
	"entriq/internal/gateway"

	"github.com/gin-gonic/gin"
)

func Routes(Router *gin.Engine, gw *gateway.Gateway) {

	/**
	* Define health probes to facilitate kubernetes health
	* checks (not proxied through gateway)
	 */
	Routes_Probes := Router.Group("/probes")
	{
		Routes_Probes.GET("/live", func(c *gin.Context) {
			c.AbortWithStatus(200)
		})

		Routes_Probes.GET("/ready", func(c *gin.Context) {
			cfg := gw.GetConfig()
			if cfg == nil || len(cfg.Services) == 0 {
				c.AbortWithStatus(503)
				return
			}
			c.AbortWithStatus(200)
		})
	}

	/**
	 * Gateway handles all other routes based on config
	 * Routes like /users, /v1.0/transaction, etc. will be checked against config
	 * If no match found, gateway returns 404 with standard error format
	 */
	Router.NoRoute(gw.ProxyHandler())

}
