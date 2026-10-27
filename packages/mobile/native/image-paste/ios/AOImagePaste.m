#import <React/RCTBridgeModule.h>
#import <React/RCTEventEmitter.h>
#import <UIKit/UIKit.h>
#import <UniformTypeIdentifiers/UniformTypeIdentifiers.h>
#import <objc/runtime.h>

// Matches the composer's 10 MB per-image limit; anything larger is reported by size, unsent.
static const NSUInteger AOMaxImageBytes = 10 * 1024 * 1024;
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

/** An item's image representations, in the pasteboard's own order. Read on main, inside the user's Paste. */
static NSArray<NSArray *> *AOImageRepresentations(UIPasteboard *pasteboard, NSInteger index)
{
  NSIndexSet *item = [NSIndexSet indexSetWithIndex:index];
  NSMutableArray<NSArray *> *representations = [NSMutableArray new];
  for (NSString *identifier in [pasteboard pasteboardTypesForItemSet:item].firstObject ?: @[]) {
    if (![[UTType typeWithIdentifier:identifier] conformsToType:UTTypeImage]) continue;
    id data = [pasteboard dataForPasteboardType:identifier inItemSet:item].firstObject;
    if ([data isKindOfClass:NSData.class]) [representations addObject:@[ identifier, data ]];
  }
  return representations;
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

/**
 * `{ mimeType, bytes, data? }` for one item; no data means too large or unreadable.
 * The first representation the agents take as-is and that fits is sent unchanged.
 * Otherwise the first decodable one is re-encoded as JPEG: HEIC or TIFF, and also a
 * PNG photo over the limit whose JPEG would fit.
 */
static NSDictionary *AOEncodeImage(NSArray<NSArray *> *representations)
{
  NSString *mimeType = nil;
  NSData *chosen = nil;
  NSUInteger smallest = 0;
  for (NSArray *representation in representations) {
    NSString *type = AOPassthroughMimeType(representation[0]);
    NSData *data = representation[1];
    if (!type) continue;
    if (data.length <= AOMaxImageBytes) { mimeType = type; chosen = data; break; }
    smallest = smallest ? MIN(smallest, data.length) : data.length;
  }
  if (!chosen) {
    for (NSArray *representation in representations) {
      UIImage *image = [UIImage imageWithData:representation[1]];
      NSData *jpeg = image ? UIImageJPEGRepresentation(image, 0.9) : nil;
      if (!jpeg) continue;
      mimeType = @"image/jpeg";
      if (jpeg.length <= AOMaxImageBytes) chosen = jpeg;
      else smallest = smallest ? MIN(smallest, jpeg.length) : jpeg.length;
      break;
    }
  }
  if (!chosen) return @{@"mimeType" : mimeType ?: @"image/jpeg", @"bytes" : @(smallest)};
  return @{@"mimeType" : mimeType, @"bytes" : @(chosen.length), @"data" : [chosen base64EncodedStringWithOptions:0]};
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
  NSMutableArray<NSArray<NSArray *> *> *items = [NSMutableArray new];
  for (NSInteger index = 0; index < pasteboard.numberOfItems; index++) {
    NSArray<NSArray *> *representations = AOImageRepresentations(pasteboard, index);
    if (representations.count) [items addObject:representations];
  }
  if (!items.count) return;
  // Decoding, re-encoding and base64 can take a moment on a large photo; keep them off the UI.
  __weak AOImagePaste *weakSelf = self;
  dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED, 0), ^{
    NSMutableArray<NSDictionary *> *images = [NSMutableArray new];
    for (NSArray<NSArray *> *representations in items) [images addObject:AOEncodeImage(representations)];
    dispatch_async(dispatch_get_main_queue(), ^{
      [weakSelf sendEventWithName:@"AOImagePaste" body:@{@"tag" : tag, @"images" : images}];
    });
  });
}

- (void)invalidate
{
  for (NSNumber *tag in _bindings.keyEnumerator.allObjects) [self detachTag:tag];
  [super invalidate];
}

@end
