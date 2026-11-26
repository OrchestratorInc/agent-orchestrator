module.exports = {
  dependency: {
    platforms: {
      ios: {
        podspecPath: `${__dirname}/AOImagePaste.podspec`,
      },
      android: {
        sourceDir: './android',
        packageImportPath: 'import ao.mobile.imagepaste.ImagePastePackage;',
        packageInstance: 'new ImagePastePackage()',
      },
    },
  },
};
