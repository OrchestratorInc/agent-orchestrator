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
  return pasteboard.hasImages && !pasteboard.hasStrings;
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

/** Known image types are sent as copied; anything else (HEIC, TIFF) is re-encoded as JPEG. */
static NSDictionary *AOReadPasteboardItem(UIPasteboard *pasteboard, NSInteger index)
{
  NSIndexSet *item = [NSIndexSet indexSetWithIndex:index];
  NSArray<NSString *> *types = [pasteboard pasteboardTypesForItemSet:item].firstObject ?: @[];
  NSArray<NSArray<NSString *> *> *passthrough = @[
    @[ UTTypePNG.identifier, @"image/png" ],
    @[ UTTypeJPEG.identifier, @"image/jpeg" ],
    @[ UTTypeGIF.identifier, @"image/gif" ],
    @[ UTTypeWebP.identifier, @"image/webp" ],
    @[ UTTypeBMP.identifier, @"image/bmp" ],
  ];
  for (NSArray<NSString *> *pair in passthrough) {
    if (![types containsObject:pair[0]]) continue;
    id data = [pasteboard dataForPasteboardType:pair[0] inItemSet:item].firstObject;
    if ([data isKindOfClass:NSData.class]) return @{@"mimeType" : pair[1], @"data" : data};
  }
  for (NSString *identifier in types) {
    UTType *type = [UTType typeWithIdentifier:identifier];
    if (![type conformsToType:UTTypeImage]) continue;
    id value = [pasteboard dataForPasteboardType:identifier inItemSet:item].firstObject;
    UIImage *image = [value isKindOfClass:NSData.class] ? [UIImage imageWithData:value] : nil;
    NSData *jpeg = image ? UIImageJPEGRepresentation(image, 0.9) : nil;
    return jpeg ? @{@"mimeType" : @"image/jpeg", @"data" : jpeg} : @{@"mimeType" : @"image/jpeg"};
  }
  return nil;
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
  NSMutableArray<NSDictionary *> *images = [NSMutableArray new];
  for (NSInteger index = 0; index < pasteboard.numberOfItems; index++) {
    NSDictionary *item = AOReadPasteboardItem(pasteboard, index);
    if (!item) continue;
    NSData *data = item[@"data"];
    NSMutableDictionary *image = [@{@"mimeType" : item[@"mimeType"], @"bytes" : @(data.length)} mutableCopy];
    if (data && data.length <= AOMaxImageBytes) image[@"data"] = [data base64EncodedStringWithOptions:0];
    [images addObject:image];
  }
  if (images.count) [self sendEventWithName:@"AOImagePaste" body:@{@"tag" : tag, @"images" : images}];
}

- (void)invalidate
{
  for (NSNumber *tag in _bindings.keyEnumerator.allObjects) [self detachTag:tag];
  [super invalidate];
}

@end
