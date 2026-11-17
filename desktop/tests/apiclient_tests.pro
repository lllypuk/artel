QT += core gui testlib network
CONFIG += c++17 testcase warn_on
QMAKE_CXXFLAGS += -Wextra -Werror
TARGET = apiclient_tests
TEMPLATE = app
INCLUDEPATH += ../src
SOURCES += apiclient_tests.cpp ../src/apiclient.cpp
HEADERS += ../src/apiclient.h
