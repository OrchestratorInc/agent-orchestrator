module.exports = {
  dependency: {
    platforms: {
      ios: null,
      android: {
        sourceDir: './android',
        packageImportPath: 'import ao.mobile.selection.ResponseSelectionPackage;',
        packageInstance: 'new ResponseSelectionPackage()',
      },
    },
  },
};
