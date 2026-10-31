import React from 'react';
import { Platform, Pressable, StyleSheet, View, useWindowDimensions } from 'react-native';
import { BlurView } from 'expo-blur';
import { GlassView, isGlassEffectAPIAvailable, isLiquidGlassAvailable } from 'expo-glass-effect';
import { colors, Icon, Txt, type IconName } from './ui';

export type MainTab = 'home' | 'practice' | 'investments' | 'money';

const tabs: { id: MainTab; label: string; icon: IconName }[] = [
  { id: 'home', label: 'Главная', icon: 'home' },
  { id: 'practice', label: 'Практика', icon: 'compass' },
  { id: 'investments', label: 'Инвестиции', icon: 'chart' },
  { id: 'money', label: 'Мои деньги', icon: 'wallet' },
];

type Props = {
  value: MainTab;
  onChange: (tab: MainTab) => void;
  disabled: boolean;
  bottom: number;
  reducedTransparency?: boolean;
};

export function IslandNav({ value, onChange, disabled, bottom, reducedTransparency = false }: Props) {
  const { width } = useWindowDimensions();
  const inset = width < 360 ? 12 : 16;
  const nativeGlass = !reducedTransparency && Platform.OS === 'ios'
    && isLiquidGlassAvailable() && isGlassEffectAPIAvailable();

  const content = <>
    <View pointerEvents="none" style={styles.edgeLight}/>
    <View accessibilityRole="tablist" accessibilityLabel="Основные разделы" style={styles.tabs}>
      {tabs.map(tab => {
        const selected = value === tab.id;
        return <Pressable
          key={tab.id}
          accessibilityRole="tab"
          accessibilityLabel={tab.label}
          accessibilityState={{ selected, disabled }}
          aria-selected={selected}
          aria-disabled={disabled}
          disabled={disabled}
          onPress={() => onChange(tab.id)}
          style={({ pressed }) => [styles.tab, selected ? styles.selected : pressed ? styles.pressed : null]}
        >
          <Icon name={tab.icon} size={23} stroke={selected ? 2.2 : 1.8} color={selected ? colors.deepBlue : '#3F4C65'}/>
          <Txt size={11} weight={selected ? 'extra' : 'bold'} color={selected ? colors.deepBlue : '#3F4C65'} numberOfLines={1} style={styles.label}>{tab.label}</Txt>
        </Pressable>;
      })}
    </View>
  </>;

  return <View pointerEvents="box-none" style={[styles.position, { left: inset, right: inset, bottom }]}>
    <View style={styles.shadow}>
      {nativeGlass
        ? <GlassView glassEffectStyle="regular" colorScheme="light" tintColor="#EAF2FF28" isInteractive style={styles.surface}>{content}</GlassView>
        : !reducedTransparency && (Platform.OS === 'ios' || Platform.OS === 'web')
          ? <BlurView tint="light" intensity={65} style={styles.surface}>{content}</BlurView>
          : <View style={[styles.surface, { backgroundColor: reducedTransparency ? '#F4F7FD' : 'rgba(242,247,255,0.92)' }]}>{content}</View>}
    </View>
  </View>;
}

const styles = StyleSheet.create({
  position: { position: 'absolute', height: 78, zIndex: 20 },
  shadow: {
    flex: 1,
    borderRadius: 30,
    backgroundColor: 'rgba(231,240,255,0.12)',
    ...Platform.select({
      web: { boxShadow: '0 8px 24px rgba(23,48,96,0.18)' },
      default: { shadowColor: '#173060', shadowOpacity: 0.18, shadowRadius: 18, shadowOffset: { width: 0, height: 8 }, elevation: 10 },
    }),
  },
  surface: {
    flex: 1,
    borderRadius: 30,
    overflow: 'hidden',
    borderWidth: 1,
    borderColor: 'rgba(255,255,255,0.8)',
  },
  edgeLight: { position: 'absolute', top: 0, left: 30, right: 30, height: 1, backgroundColor: 'rgba(255,255,255,0.9)' },
  tabs: { flex: 1, flexDirection: 'row', paddingHorizontal: 4, paddingVertical: 7 },
  tab: {
    flex: 1,
    minWidth: 44,
    minHeight: 44,
    borderRadius: 23,
    borderWidth: 1,
    borderColor: 'transparent',
    justifyContent: 'center',
    alignItems: 'center',
    gap: 4,
    paddingHorizontal: 0,
  },
  selected: { backgroundColor: 'rgba(219,232,255,0.88)', borderColor: 'rgba(38,101,255,0.24)' },
  pressed: { backgroundColor: 'rgba(255,255,255,0.38)' },
  label: { textAlign: 'center', letterSpacing: -0.2 },
});
