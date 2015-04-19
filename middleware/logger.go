package middleware

import (
	"log"
	"time"

	"github.com/labstack/echo"
	"github.com/labstack/gommon/color"
)

func Logger(h echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		start := time.Now()
		if err := h(c); err != nil {
			return err
		}
		end := time.Now()
		col := color.Green
		m := c.Request.Method
		p := c.Request.URL.Path
		n := c.Response.Status()

		switch {
		case n >= 500:
			col = color.Red
		case n >= 400:
			col = color.Yellow
		case n >= 300:
			col = color.Cyan
		}

		log.Printf("%s %s %s %s", m, p, col(n), end.Sub(start))
		return nil
	}
}
