#import <React/RCTBridgeModule.h>
#include <arpa/inet.h>
#include <fcntl.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <unistd.h>

// A one-shot HTTP listener on 127.0.0.1 for a coding agent's sign-in.
//
// Claude Code and Codex finish their browser sign-in by redirecting to
// http://localhost:<port>/<path>?code=…, the redirect their OAuth clients are
// registered for (the desktop catches it the same way). The sign-in runs in the
// system browser sheet, which shares the person's Safari sessions; this
// listener receives that redirect, answers it with a redirect back into the app
// (which closes the sheet), and hands the request target to JavaScript.
@interface AOOAuthLoopback : NSObject <RCTBridgeModule>
@end

@implementation AOOAuthLoopback {
  dispatch_queue_t _queue;
  dispatch_source_t _source;
  NSString *_callbackPath;
  NSString *_returnURL;
  NSString *_pending;
  RCTPromiseResolveBlock _resolve;
  RCTPromiseRejectBlock _reject;
}

RCT_EXPORT_MODULE(AOOAuthLoopback)

+ (BOOL)requiresMainQueueSetup
{
  return NO;
}

- (instancetype)init
{
  if (self = [super init]) {
    _queue = dispatch_queue_create("ao.oauth-loopback", DISPATCH_QUEUE_SERIAL);
  }
  return self;
}

- (void)invalidate
{
  dispatch_async(_queue, ^{ [self stopLocked:@"The app is closing."]; });
}

// Must run on _queue.
- (void)stopLocked:(NSString *)reason
{
  if (_source) {
    dispatch_source_cancel(_source);
    _source = nil;
  }
  if (_reject) {
    _reject(@"cancelled", reason, nil);
  }
  _resolve = nil;
  _reject = nil;
  _pending = nil;
}

static void AOWrite(int fd, NSString *text)
{
  NSData *data = [text dataUsingEncoding:NSUTF8StringEncoding];
  const uint8_t *bytes = data.bytes;
  NSUInteger left = data.length;
  while (left > 0) {
    ssize_t written = write(fd, bytes, left);
    if (written <= 0) return;
    bytes += written;
    left -= (NSUInteger)written;
  }
}

static NSString *AOEscapeHTML(NSString *text)
{
  return [[[text stringByReplacingOccurrencesOfString:@"&" withString:@"&amp;"]
    stringByReplacingOccurrencesOfString:@"\"" withString:@"&quot;"]
    stringByReplacingOccurrencesOfString:@"<" withString:@"&lt;"];
}

// Must run on _queue.
- (void)acceptOn:(int)listener
{
  int client = accept(listener, NULL, NULL);
  if (client < 0) return;
  int flags = fcntl(client, F_GETFL, 0);
  fcntl(client, F_SETFL, flags & ~O_NONBLOCK);
  struct timeval timeout = { .tv_sec = 3, .tv_usec = 0 };
  setsockopt(client, SOL_SOCKET, SO_RCVTIMEO, &timeout, sizeof(timeout));
  int noSigPipe = 1;
  setsockopt(client, SOL_SOCKET, SO_NOSIGPIPE, &noSigPipe, sizeof(noSigPipe));

  char buffer[8192];
  ssize_t length = read(client, buffer, sizeof(buffer) - 1);
  if (length <= 0) {
    close(client);
    return;
  }
  buffer[length] = 0;
  NSString *request = [[NSString alloc] initWithBytes:buffer length:(NSUInteger)length encoding:NSUTF8StringEncoding] ?: @"";
  NSString *line = [[request componentsSeparatedByString:@"\r\n"] firstObject] ?: @"";
  NSArray<NSString *> *parts = [line componentsSeparatedByString:@" "];
  NSString *target = parts.count >= 2 ? parts[1] : @"";
  NSString *path = [[target componentsSeparatedByString:@"?"] firstObject] ?: @"";

  if (![parts.firstObject isEqualToString:@"GET"] || ![path isEqualToString:_callbackPath]) {
    AOWrite(client, @"HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n");
    close(client);
    return;
  }

  NSString *link = AOEscapeHTML(_returnURL ?: @"");
  NSString *body = [NSString stringWithFormat:@"<!doctype html><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width\"><title>Signed in</title><body style=\"font:16px -apple-system,system-ui,sans-serif;padding:15vh 1.5rem;text-align:center\"><h1 style=\"font-size:1.25rem\">Signed in</h1><p><a href=\"%@\">Return to Orchestrator</a></p></body>", link];
  NSUInteger bodyLength = [body lengthOfBytesUsingEncoding:NSUTF8StringEncoding];
  NSString *response = _returnURL.length
    ? [NSString stringWithFormat:@"HTTP/1.1 302 Found\r\nLocation: %@\r\nContent-Type: text/html; charset=utf-8\r\nContent-Length: %lu\r\nCache-Control: no-store\r\nConnection: close\r\n\r\n%@", _returnURL, (unsigned long)bodyLength, body]
    : [NSString stringWithFormat:@"HTTP/1.1 200 OK\r\nContent-Type: text/html; charset=utf-8\r\nContent-Length: %lu\r\nCache-Control: no-store\r\nConnection: close\r\n\r\n%@", (unsigned long)bodyLength, body];
  AOWrite(client, response);
  close(client);

  if (_resolve) {
    _resolve(target);
    _resolve = nil;
    _reject = nil;
  } else {
    _pending = target;
  }
}

RCT_EXPORT_METHOD(start:(double)port
                  callbackPath:(NSString *)callbackPath
                  returnURL:(NSString *)returnURL
                  resolve:(RCTPromiseResolveBlock)resolve
                  reject:(RCTPromiseRejectBlock)reject)
{
  dispatch_async(_queue, ^{
    [self stopLocked:@"A new sign-in started."];
    int listener = socket(AF_INET, SOCK_STREAM, 0);
    if (listener < 0) {
      reject(@"socket", @"Couldn't open a local socket for the sign-in.", nil);
      return;
    }
    int yes = 1;
    setsockopt(listener, SOL_SOCKET, SO_REUSEADDR, &yes, sizeof(yes));
    struct sockaddr_in address;
    memset(&address, 0, sizeof(address));
    address.sin_len = sizeof(address);
    address.sin_family = AF_INET;
    address.sin_port = htons((uint16_t)port);
    address.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    if (bind(listener, (struct sockaddr *)&address, sizeof(address)) != 0) {
      close(listener);
      reject(@"in_use", @"The sign-in port on this phone is busy.", nil);
      return;
    }
    if (listen(listener, 8) != 0) {
      close(listener);
      reject(@"listen", @"Couldn't listen for the sign-in.", nil);
      return;
    }
    socklen_t size = sizeof(address);
    getsockname(listener, (struct sockaddr *)&address, &size);
    fcntl(listener, F_SETFL, O_NONBLOCK);

    self->_callbackPath = callbackPath;
    self->_returnURL = returnURL;
    dispatch_source_t source = dispatch_source_create(DISPATCH_SOURCE_TYPE_READ, (uintptr_t)listener, 0, self->_queue);
    __weak AOOAuthLoopback *weakSelf = self;
    dispatch_source_set_event_handler(source, ^{ [weakSelf acceptOn:listener]; });
    dispatch_source_set_cancel_handler(source, ^{ close(listener); });
    self->_source = source;
    dispatch_resume(source);
    resolve(@(ntohs(address.sin_port)));
  });
}

RCT_EXPORT_METHOD(waitForCallback:(RCTPromiseResolveBlock)resolve
                  reject:(RCTPromiseRejectBlock)reject)
{
  dispatch_async(_queue, ^{
    if (self->_pending) {
      resolve(self->_pending);
      self->_pending = nil;
      return;
    }
    if (!self->_source) {
      reject(@"not_started", @"The sign-in listener isn't running.", nil);
      return;
    }
    if (self->_reject) self->_reject(@"cancelled", @"Superseded by a newer wait.", nil);
    self->_resolve = resolve;
    self->_reject = reject;
  });
}

RCT_EXPORT_METHOD(stop)
{
  dispatch_async(_queue, ^{ [self stopLocked:@"Sign-in cancelled."]; });
}

@end
