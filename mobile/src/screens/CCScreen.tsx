import React, { useEffect, useState } from 'react';
import { View, StyleSheet, ActivityIndicator } from 'react-native';
import { WebView } from 'react-native-webview';
import { getServerUrl, getToken } from '../services/auth';

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
        <ActivityIndicator size="large" color="#6c63ff" />
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
            <ActivityIndicator size="large" color="#6c63ff" />
          </View>
        )}
      />
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: '#1a1a2e',
  },
  webview: {
    flex: 1,
  },
  loading: {
    flex: 1,
    justifyContent: 'center',
    alignItems: 'center',
    backgroundColor: '#1a1a2e',
  },
});
