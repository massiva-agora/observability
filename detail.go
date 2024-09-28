package observability

import (
	"github.com/gofiber/fiber/v2"
)

type ProblemDetail struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

func NewProblemDetail(c *fiber.Ctx, d ProblemDetail) error {
	c.Set(fiber.HeaderContentType, "application/problem+json")
	return c.Status(d.Status).JSON(d)
}
