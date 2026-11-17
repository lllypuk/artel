QT += core gui qml quick quickcontrols2
CONFIG += c++17 release warn_on
QMAKE_CXXFLAGS += -Wextra -Werror
TARGET = artel
TEMPLATE = app
SOURCES += src/main.cpp
RESOURCES += src/resources.qrc
