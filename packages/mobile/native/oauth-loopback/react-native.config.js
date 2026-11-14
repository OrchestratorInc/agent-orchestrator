module.exports = {
  dependency: {
    platforms: {
      ios: {
        podspecPath: `${__dirname}/AOOAuthLoopback.podspec`,
      },
      android: {
        sourceDir: './android',
        packageImportPath: 'import ao.mobile.oauthloopback.OAuthLoopbackPackage;',
        packageInstance: 'new OAuthLoopbackPackage()',
      },
    },
  },
};
