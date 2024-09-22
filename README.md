# Usage

In main():

```
ctx := context.Background()
app, shutdown := NewLogrusAndTraceAwareFiberApp(ctx, "mailgun-api")
[...]
SafeShutdown(app.Listen(":8080"), shutdown(ctx))
```

And then inside the request handlers:

```
logger := GetLogger(c)
```
