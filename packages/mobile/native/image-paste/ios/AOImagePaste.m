#import <React/RCTBridgeModule.h>
#import <React/RCTEventEmitter.h>
#import <UIKit/UIKit.h>
#import <UniformTypeIdentifiers/UniformTypeIdentifiers.h>
#import <objc/runtime.h>

// Matches the composer's 10 MB per-image limit; anything larger is reported by size, unsent.
static const NSUInteger AOMaxImageBytes = 10 * 1024 * 1024;
// The composer's batch limits (8 attachments, 25 MB of images). Applied here too, so
// a large multi-photo copy isn't read and encoded only to be discarded in JS.
static const NSUInteger AOMaxImages = 8;
static const NSUInteger AOMaxBatchBytes = 25 * 1024 * 1024;
static char AOImagePasteTagKey;
static char AOImagePasteModuleKey;
static IMP AOOriginalPaste;
static IMP AOOriginalCanPerformAction;

@interface AOImagePaste : RCTEventEmitter <RCTBridgeModule>
- (void)pasteImagesForTag:(NSNumber *)tag;
@end

/** Weak box: an associated object can't be weak, and the module may go away first. */
@interface AOImagePasteModuleRef : NSObject
@property (nonatomic, weak) AOImagePaste *module;
@end
@implementation AOImagePasteModuleRef
@end

static BOOL AOAcceptsImagePaste(UITextView *view)
{
  if (!objc_getAssociatedObject(view, &AOImagePasteTagKey) || !view.isEditable) return NO;
  if ([[view valueForKey:@"contextMenuHidden"] boolValue]) return NO;
  // Only an image-only clipboard. Spreadsheet cells and rich copies carry an
  // image rendering beside their text, and those must still paste as text.
  UIPasteboard *pasteboard = UIPasteboard.generalPasteboard;
  if (pasteboard.hasStrings) return NO;
  if (pasteboard.hasImages) return YES;
  // `hasImages` doesn't count every image type (HEIC, for one). Type names are
  // detection-only, so checking them raises no paste prompt.
  for (NSString *identifier in pasteboard.pasteboardTypes) {
    if ([[UTType typeWithIdentifier:identifier] conformsToType:UTTypeImage]) return YES;
  }
  return NO;
}

// RCTUITextView's own `paste:`. Images go to JS as attachments; text pastes as usual.
static void AOPaste(UITextView *self, SEL _cmd, id sender)
{
  NSNumber *tag = objc_getAssociatedObject(self, &AOImagePasteTagKey);
  AOImagePaste *module = ((AOImagePasteModuleRef *)objc_getAssociatedObject(self, &AOImagePasteModuleKey)).module;
  if (tag && module && AOAcceptsImagePaste(self)) {
    [module pasteImagesForTag:tag];
    return;
  }
  ((void (*)(id, SEL, id))AOOriginalPaste)(self, _cmd, sender);
}

// UITextView only offers Paste for text it can insert; an image-only clipboard needs saying yes.
static BOOL AOCanPerformAction(UITextView *self, SEL _cmd, SEL action, id sender)
{
  BOOL original = ((BOOL (*)(id, SEL, SEL, id))AOOriginalCanPerformAction)(self, _cmd, action, sender);
  if (original || action != @selector(paste:)) return original;
  return AOAcceptsImagePaste(self);
}

static void AOInstallPasteHooks(void)
{
  static dispatch_once_t once;
  dispatch_once(&once, ^{
    Class textView = NSClassFromString(@"RCTUITextView");
    Method paste = class_getInstanceMethod(textView, @selector(paste:));
    Method canPerform = class_getInstanceMethod(textView, @selector(canPerformAction:withSender:));
    if (!paste || !canPerform) return;
    AOOriginalPaste = method_setImplementation(paste, (IMP)AOPaste);
    AOOriginalCanPerformAction = method_setImplementation(canPerform, (IMP)AOCanPerformAction);
  });
}

static UITextView *AOFindTextView(UIView *view)
{
  Class textView = NSClassFromString(@"RCTUITextView");
  if ([view isKindOfClass:textView]) return (UITextView *)view;
  for (UIView *child in view.subviews) {
    UITextView *found = AOFindTextView(child);
    if (found) return found;
  }
  return nil;
}

static NSString *AOPassthroughMimeType(NSString *identifier)
{
  static NSDictionary<NSString *, NSString *> *types;
  static dispatch_once_t once;
  dispatch_once(&once, ^{
    types = @{
      UTTypePNG.identifier : @"image/png",
      UTTypeJPEG.identifier : @"image/jpeg",
      UTTypeGIF.identifier : @"image/gif",
      UTTypeWebP.identifier : @"image/webp",
      UTTypeBMP.identifier : @"image/bmp",
    };
  });
  return types[identifier];
}

/** Whether an item holds an image, from its type names alone: nothing is read. */
static BOOL AOItemHasImage(UIPasteboard *pasteboard, NSInteger index)
{
  for (NSString *identifier in [pasteboard pasteboardTypesForItemSet:[NSIndexSet indexSetWithIndex:index]].firstObject ?: @[]) {
    if ([[UTType typeWithIdentifier:identifier] conformsToType:UTTypeImage]) return YES;
  }
  return NO;
}

/**
 * Reads one item on main, inside the user's Paste, and only as far as needed:
 * representations are produced on demand, and some are large (the TIFF of a Retina
 * screenshot over Universal Clipboard). Walks the types in the pasteboard's order
 * and stops at the first one the agents take as-is that fits. Failing that, keeps
 * one source to re-encode as JPEG: the first oversized one already read, else the
 * item's first image type (HEIC, TIFF). nil when the item holds no image.
 */
static NSDictionary *AOReadImageItem(UIPasteboard *pasteboard, NSInteger index)
{
  NSIndexSet *item = [NSIndexSet indexSetWithIndex:index];
  NSString *firstImageType = nil;
  NSData *source = nil;
  NSUInteger oversized = 0;
  for (NSString *identifier in [pasteboard pasteboardTypesForItemSet:item].firstObject ?: @[]) {
    if (![[UTType typeWithIdentifier:identifier] conformsToType:UTTypeImage]) continue;
    if (!firstImageType) firstImageType = identifier;
    NSString *mimeType = AOPassthroughMimeType(identifier);
    if (!mimeType) continue;
    NSData *data = [pasteboard dataForPasteboardType:identifier inItemSet:item].firstObject;
    if (![data isKindOfClass:NSData.class]) continue;
    if (data.length <= AOMaxImageBytes) return @{@"mimeType" : mimeType, @"data" : data};
    oversized = oversized ? MIN(oversized, data.length) : data.length;
    if (!source) source = data;
  }
  if (!firstImageType) return nil;
  if (!source) {
    NSData *data = [pasteboard dataForPasteboardType:firstImageType inItemSet:item].firstObject;
    if ([data isKindOfClass:NSData.class]) source = data;
  }
  NSMutableDictionary *read = [@{@"oversized" : @(oversized)} mutableCopy];
  if (source) read[@"source"] = source;
  return read;
}

/**
 * `{ mimeType, bytes, data? }` for one read item; no data means too large or
 * unreadable. Runs off main: base64, and for a re-encode the decode and JPEG.
 */
static NSDictionary *AOEncodeImage(NSDictionary *read)
{
  NSData *data = read[@"data"];
  if (data) return @{@"mimeType" : read[@"mimeType"], @"bytes" : @(data.length), @"data" : [data base64EncodedStringWithOptions:0]};
  UIImage *image = read[@"source"] ? [UIImage imageWithData:read[@"source"]] : nil;
  NSData *jpeg = image ? UIImageJPEGRepresentation(image, 0.9) : nil;
  if (jpeg.length && jpeg.length <= AOMaxImageBytes) return @{@"mimeType" : @"image/jpeg", @"bytes" : @(jpeg.length), @"data" : [jpeg base64EncodedStringWithOptions:0]};
  NSUInteger oversized = [read[@"oversized"] unsignedIntegerValue];
  NSUInteger bytes = jpeg.length ? (oversized ? MIN(oversized, jpeg.length) : jpeg.length) : oversized;
  return @{@"mimeType" : @"image/jpeg", @"bytes" : @(bytes)};
}

@implementation AOImagePaste {
  NSMapTable<NSNumber *, UITextView *> *_bindings;
}

@synthesize viewRegistry_DEPRECATED = _viewRegistry_DEPRECATED;

RCT_EXPORT_MODULE(AOImagePaste)

+ (BOOL)requiresMainQueueSetup { return YES; }
- (dispatch_queue_t)methodQueue { return dispatch_get_main_queue(); }
- (NSArray<NSString *> *)supportedEvents { return @[ @"AOImagePaste" ]; }

- (instancetype)init
{
  if (self = [super init]) _bindings = [NSMapTable strongToWeakObjectsMapTable];
  return self;
}

RCT_EXPORT_METHOD(attach:(nonnull NSNumber *)tag)
{
  AOInstallPasteHooks();
  UITextView *view = AOFindTextView([self.viewRegistry_DEPRECATED viewForReactTag:tag]);
  if (!view) return;
  [self detachTag:tag];
  AOImagePasteModuleRef *ref = [AOImagePasteModuleRef new];
  ref.module = self;
  objc_setAssociatedObject(view, &AOImagePasteTagKey, tag, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
  objc_setAssociatedObject(view, &AOImagePasteModuleKey, ref, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
  [_bindings setObject:view forKey:tag];
}

RCT_EXPORT_METHOD(detach:(nonnull NSNumber *)tag)
{
  [self detachTag:tag];
}

- (void)detachTag:(NSNumber *)tag
{
  UITextView *view = [_bindings objectForKey:tag];
  [_bindings removeObjectForKey:tag];
  // Fabric recycles text inputs; only clear the binding if it is still ours.
  if (view && [objc_getAssociatedObject(view, &AOImagePasteTagKey) isEqual:tag]) {
    objc_setAssociatedObject(view, &AOImagePasteTagKey, nil, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
    objc_setAssociatedObject(view, &AOImagePasteModuleKey, nil, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
  }
}

- (void)pasteImagesForTag:(NSNumber *)tag
{
  UIPasteboard *pasteboard = UIPasteboard.generalPasteboard;
  NSMutableArray<NSDictionary *> *items = [NSMutableArray new];
  NSUInteger held = 0;
  __block NSString *overflow = nil;
  for (NSInteger index = 0; index < pasteboard.numberOfItems; index++) {
    if (!AOItemHasImage(pasteboard, index)) continue;
    if (items.count >= AOMaxImages) { overflow = @"count"; break; }
    NSDictionary *read = AOReadImageItem(pasteboard, index);
    if (!read) continue;
    NSUInteger bytes = ((NSData *)(read[@"data"] ?: read[@"source"])).length;
    // The first image is kept whatever its size: it is already read, and its JPEG may fit.
    if (items.count && held + bytes > AOMaxBatchBytes) { overflow = @"size"; break; }
    held += bytes;
    [items addObject:read];
  }
  if (!items.count) return;
  // Decoding, re-encoding and base64 can take a moment on a large photo; keep them off the UI.
  __weak AOImagePaste *weakSelf = self;
  dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED, 0), ^{
    NSMutableArray<NSDictionary *> *images = [NSMutableArray new];
    NSUInteger sent = 0;
    for (NSDictionary *read in items) {
      NSDictionary *image = AOEncodeImage(read);
      NSUInteger bytes = image[@"data"] ? [image[@"bytes"] unsignedIntegerValue] : 0;
      if (images.count && sent + bytes > AOMaxBatchBytes) { overflow = overflow ?: @"size"; break; }
      sent += bytes;
      [images addObject:image];
    }
    NSMutableDictionary *body = [@{@"tag" : tag, @"images" : images} mutableCopy];
    if (overflow) body[@"overflow"] = overflow;
    dispatch_async(dispatch_get_main_queue(), ^{
      [weakSelf sendEventWithName:@"AOImagePaste" body:body];
    });
  });
}

- (void)invalidate
{
  for (NSNumber *tag in _bindings.keyEnumerator.allObjects) [self detachTag:tag];
  [super invalidate];
}

@end
