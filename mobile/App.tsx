import 'react-native-url-polyfill/auto';
import React, { useEffect, useState } from 'react';
import { NavigationContainer, DefaultTheme } from '@react-navigation/native';
import { createBottomTabNavigator } from '@react-navigation/bottom-tabs';
import { StatusBar } from 'expo-status-bar';
import { Ionicons } from '@expo/vector-icons';

import ChatScreen from './src/screens/ChatScreen';
import CCScreen from './src/screens/CCScreen';
import SetupScreen from './src/screens/SetupScreen';
import SettingsScreen from './src/screens/SettingsScreen';
import { isConfigured } from './src/services/auth';
import { colors } from './src/theme';

const Tab = createBottomTabNavigator();

const DarkTheme = {
  ...DefaultTheme,
  dark: true,
  colors: {
    ...DefaultTheme.colors,
    primary: colors.accent,
    background: colors.bg,
    card: colors.bg,
    text: colors.textPrimary,
    border: colors.separator,
    notification: colors.accent,
  },
};

export default function App() {
  const [configured, setConfigured] = useState<boolean | null>(null);

  useEffect(() => {
    isConfigured().then(setConfigured);
  }, []);

  if (configured === null) return null;

  if (!configured) {
    return (
      <>
        <StatusBar style="light" />
        <SetupScreen onComplete={() => setConfigured(true)} />
      </>
    );
  }

  return (
    <>
      <StatusBar style="light" />
      <NavigationContainer theme={DarkTheme}>
        <Tab.Navigator
          screenOptions={{
            headerStyle: {
              backgroundColor: colors.bg,
              shadowColor: 'transparent',
              elevation: 0,
            },
            headerTitleStyle: {
              fontSize: 17,
              fontWeight: '600',
              color: colors.textPrimary,
            },
            tabBarStyle: {
              backgroundColor: colors.bg,
              borderTopColor: colors.separator,
              borderTopWidth: 0.5,
            },
            tabBarActiveTintColor: colors.accent,
            tabBarInactiveTintColor: colors.textTertiary,
            tabBarLabelStyle: {
              fontSize: 10,
              fontWeight: '500',
            },
          }}
        >
          <Tab.Screen
            name="Chat"
            component={ChatScreen}
            options={{
              headerTitle: 'ALF',
              tabBarIcon: ({ color, size }) => (
                <Ionicons name="chatbubble-ellipses" size={size} color={color} />
              ),
            }}
          />
          <Tab.Screen
            name="Console"
            component={CCScreen}
            options={{
              tabBarIcon: ({ color, size }) => (
                <Ionicons name="terminal" size={size} color={color} />
              ),
            }}
          />
          <Tab.Screen
            name="Settings"
            options={{
              tabBarIcon: ({ color, size }) => (
                <Ionicons name="cog" size={size} color={color} />
              ),
            }}
          >
            {() => <SettingsScreen onDisconnect={() => setConfigured(false)} />}
          </Tab.Screen>
        </Tab.Navigator>
      </NavigationContainer>
    </>
  );
}
