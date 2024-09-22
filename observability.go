package observability

import (
	"context"
	"errors"
	"fmt"
	"os"

	"cloud.google.com/go/compute/metadata"

	"github.com/gofiber/contrib/otelfiber"
	"github.com/gofiber/fiber/v2"
	"github.com/mikhail-bigun/fiberlogrus"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"

	texporter "github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/trace"
	"go.opentelemetry.io/contrib/detectors/gcp"
	"go.opentelemetry.io/contrib/propagators/autoprop"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
)

func NewLogrusAndTraceAwareFiberApp(ctx context.Context, serviceName string) (*fiber.App, func(context.Context) error) {
	logrus.SetOutput(os.Stdout)
	logrus.SetFormatter(&logrus.JSONFormatter{})

	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
	})

	app.Use(fiberlogrus.New())
	app.Use(otelfiber.Middleware())
	app.Use(newTraceAwareLogrusLoggerMiddleware())

	shutdown, err := setupOpenTelemetry(ctx, serviceName)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"event": "FailedToSetupOpenTelemetry",
		}).Fatal(err)
	}

	logrus.WithFields(logrus.Fields{
		"event": "StartUp",
	}).Info()

	return app, shutdown
}

func SafeShutdown(errs ...error) {
	if err := errors.Join(errs...); err != nil {
		logrus.Fatal(err)
	}
}

func getGcpProjectID(ctx context.Context) string {
	projectID, err := metadata.ProjectIDWithContext(ctx)
	if err != nil {
		// We're not in the cloud either, so must be unit tests
		projectID = ""
	}
	return projectID
}

// If this logger is created within a context that has OpenTelemetry tracing
// information attached, incorporate that into the logger in a format designed
// for GCP's Cloud Trace.
func newTraceAwareLogrusLogger(ctx context.Context) *logrus.Entry {
	span := oteltrace.SpanFromContext(ctx)
	spanContext := span.SpanContext()
	logger := logrus.NewEntry(logrus.New())

	if spanContext.IsValid() {
		traceId := "projects/" + getGcpProjectID(ctx) + "/traces/" + spanContext.TraceID().String()
		logger = logrus.WithFields(logrus.Fields{
			"logging.googleapis.com/trace":        traceId,
			"logging.googleapis.com/spanId":       spanContext.SpanID().String(),
			"logging.googleapis.com/traceSampled": spanContext.IsSampled(),
		})
	}
	return logger
}

func newTraceAwareLogrusLoggerMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Locals("logger", newTraceAwareLogrusLogger(c.UserContext()))
		return c.Next()
	}
}

func GetLogger(c *fiber.Ctx) *logrus.Entry {
	if logger, ok := c.Locals("logger").(*logrus.Entry); ok {
		return logger
	}

	return logrus.NewEntry(logrus.New())
}

func setupOpenTelemetry(ctx context.Context, serviceName string) (shutdown func(context.Context) error, err error) {
	var res *resource.Resource
	var traceExporter trace.SpanExporter

	if os.Getenv("ENABLE_GCP_TRACING") == "true" {
		traceExporter, err = texporter.New(texporter.WithProjectID(getGcpProjectID(ctx)))
		if err != nil {
			return nil, fmt.Errorf("failed to create resource: %w", err)
		}

		res, err = resource.New(ctx,
			resource.WithDetectors(gcp.NewDetector()),
			resource.WithTelemetrySDK(),
			resource.WithAttributes(
				semconv.ServiceNameKey.String(serviceName),
			),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create resource: %w", err)
		}
	} else {
		res, err = resource.New(ctx,
			resource.WithAttributes(
				semconv.ServiceNameKey.String(serviceName),
			),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create resource: %w", err)
		}

		traceExporter, err = otlptracegrpc.New(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to create trace exporter: %w", err)
		}
	}

	otel.SetTextMapPropagator(autoprop.NewTextMapPropagator())

	tracerProvider := trace.NewTracerProvider(
		trace.WithBatcher(traceExporter),
		trace.WithResource(res),
	)

	otel.SetTracerProvider(tracerProvider)

	shutdown = func(ctx context.Context) error {
		var errs []error
		if err := tracerProvider.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
		if len(errs) > 0 {
			return fmt.Errorf("failed to shutdown: %v", errs)
		}
		return nil
	}

	return shutdown, nil
}
