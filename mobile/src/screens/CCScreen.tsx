import React, { useEffect, useState } from 'react';
import { View, StyleSheet, ActivityIndicator } from 'react-native';
import { WebView } from 'react-native-webview';
import { getServerUrl, getToken } from '../services/auth';
import { colors } from '../theme';

export default function CCScreen() {
  const [url, setUrl] = useState<string | null>(null);

  useEffect(() => {
    (async () => {
      const serverUrl = await getServerUrl();
      const token = await getToken();
      if (serverUrl && token) {
        setUrl(`${serverUrl}/?token=${token}`);
      }
    })();
  }, []);

  if (!url) {
    return (
      <View style={styles.loading}>
        <ActivityIndicator size="small" color={colors.textSecondary} />
      </View>
    );
  }

  return (
    <View style={styles.container}>
      <WebView
        source={{ uri: url }}
        style={styles.webview}
        javaScriptEnabled
        domStorageEnabled
        startInLoadingState
        renderLoading={() => (
          <View style={styles.loading}>
            <ActivityIndicator size="small" color={colors.textSecondary} />
          </View>
        )}
      />
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: colors.bg,
  },
  webview: {
    flex: 1,
  },
  loading: {
    flex: 1,
    justifyContent: 'center',
    alignItems: 'center',
    backgroundColor: colors.bg,
  },
});
