#include <assert.h>
#include <stdio.h>
#include <string.h>
#include <expat.h>

/* CVE-2026-77214: exercise the installed library, not just its version. */
int main(void) {
  for (int final = 0; final <= 1; ++final) {
    XML_Parser parser = XML_ParserCreate(NULL);
    assert(parser != NULL);
    void *buffer = XML_GetBuffer(parser, 10);
    assert(buffer != NULL);

    assert(XML_ParseBuffer(parser, 10000, final) == XML_STATUS_ERROR);
    assert(XML_GetErrorCode(parser) == XML_ERROR_INVALID_ARGUMENT);

    memcpy(buffer, "<r></r>", 7);
    assert(XML_ParseBuffer(parser, 7, XML_FALSE) == XML_STATUS_OK);
    assert(XML_ParseBuffer(parser, 10000, final) == XML_STATUS_ERROR);
    assert(XML_GetErrorCode(parser) == XML_ERROR_INVALID_ARGUMENT);
    XML_ParserFree(parser);
  }
  printf("Expat %s rejects oversized buffers\n", XML_ExpatVersion());
  return 0;
}
