package main

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/gin-gonic/gin"
)

func Routes(Router *gin.Engine) {

	Routes_v1 := Router.Group("/v1.0")
	{
		Routes_v1.GET("/ping", func(c *gin.Context) {
			c.AbortWithStatus(200)
		})
	}

	/**
	* Define health probes to facilitate kubernetes health
	* checks
	 */
	Routes_Probes := Router.Group("/probes")
	{
		Routes_Probes.GET("/live", func(c *gin.Context) {
			c.AbortWithStatus(200)
		})

		Routes_Probes.GET("/ready", func(c *gin.Context) {
			db := c.MustGet("db").(*gorm.DB)

			/**
			* Check database connection to see if it's live
			* or not
			 */
			ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
			defer cancel()

			connection, err := db.DB()
			if err != nil {
				c.AbortWithStatus(500)
				return
			}

			if err := connection.PingContext(ctx); err != nil {
				c.AbortWithStatus(500)
				return
			}

			c.AbortWithStatus(200)
		})

	}

	/*
	* We have to show resource not found error if some
	* application request undefined route.
	 */
	Router.NoRoute(func(c *gin.Context) {
		c.AbortWithStatusJSON(404, gin.H{
			"status": "failed",
			"error": gin.H{
				"code":    404,
				"message": "Resource not found",
			},
		})
	})

}
